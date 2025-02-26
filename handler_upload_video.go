package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

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

	_, err = io.Copy(tmpfile, formFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong while writing file", err)
		return
	}

	_, err = tmpfile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong while writing file", err)
		return
	}
	key := fmt.Sprintf("%s.mp4", videoID)
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

	*videoMetaData.VideoURL = fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong updating db entry", err)
		return
	}
	respondWithJSON(w, http.StatusOK, videoMetaData)
}
