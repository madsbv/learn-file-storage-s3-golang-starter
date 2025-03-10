package main

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/exp/constraints"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Video with id %s not found", videoID), err)
		return
	}

	if videoMetaData.CreateVideoParams.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("User %s does not own video %s", userID, videoID), err)
		return
	}

	formFile, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Video data not present", err)
		return
	}
	defer formFile.Close()

	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid Content-Type header: %s", contentType), err)
		return
	}
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Given content type is not video/mp4", mediatype), nil)
		return
	}

	tmpfile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong", err)
		return
	}
	defer os.Remove("tubely-upload.mp4")
	defer tmpfile.Close()

	// Write uploaded video to disk temporarily
	_, err = io.Copy(tmpfile, formFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong while writing file", err)
		return
	}

	processedPath, err := processVideoForFastStart(tmpfile.Name())
	processedFile := tmpfile
	if err != nil {
		log.Printf("Failed to process for fast start, continuing with original: %s\n%s\n", tmpfile.Name(), err)
	} else {
		processedFile, err = os.Open(processedPath)
		if err != nil {
			log.Printf("Failed to open fast start processed file, continuing with original: %s", err)
		} else {
			defer processedFile.Close()
			tmpfile = processedFile
		}
	}

	// Reset cursor to copy tmpfile to s3
	_, err = tmpfile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong while writing file", err)
		return
	}

	prefix, err := getVideoAspectRatio(tmpfile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong while writing file", err)
		return
	}

	key := fmt.Sprintf("%s/%s.mp4", prefix, videoID)
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        tmpfile,
		ContentType: &mediatype,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong uploading to S3", err)
		return
	}

	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
	videoMetaData.VideoURL = &url
	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong updating db entry", err)
		return
	}
	respondWithJSON(w, http.StatusOK, videoMetaData)
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := fmt.Sprintf("ffprobe -v error -print_format json -show_streams %s | jq '.streams.[0].height'", filePath)
	s := exec.Command("zsh", "-c", cmd)
	out, err := s.Output()
	fmt.Println(strings.TrimSpace(string(out[:])), err)
	if err != nil {
		return "", err
	}
	height, err := strconv.Atoi(strings.TrimSpace(string(out[:])))
	if err != nil {
		return "", err
	}

	cmd = fmt.Sprintf("ffprobe -v error -print_format json -show_streams %s | jq '.streams.[0].width'", filePath)
	s = exec.Command("zsh", "-c", cmd)
	out, err = s.Output()
	fmt.Println(strings.TrimSpace(string(out[:])), err)
	if err != nil {
		return "", err
	}
	width, err := strconv.Atoi(strings.TrimSpace(string(out[:])))
	if err != nil {
		return "", err
	}

	if Abs(width*9-height*16) < 16 {
		// landscape: width/height = 16/9
		return "landscape", nil
	} else if Abs(height*9-width*16) < 16 {
		// portrait: height/width = 16/9
		return "portrait", nil
	} else {
		return "other", nil
	}
}

func Abs[T constraints.Integer](x T) T {
	if x < 0 {
		return -x
	}
	return x
}

func processVideoForFastStart(filePath string) (string, error) {
	processing := filePath + ".processing"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", processing)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return processing, nil
}
