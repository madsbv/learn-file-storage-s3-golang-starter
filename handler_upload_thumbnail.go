package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form data", err)
		return
	}

	formFile, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Thumbnail data not present", err)
		return
	}

	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid Content-Type header: %s", contentType), err)
		return
	}
	filetype := strings.Split(mediatype, "/")
	if len(filetype) != 2 || filetype[0] != "image" {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Given content type is not a valid image format", filetype), nil)
		return
	}

	data, err := io.ReadAll(formFile)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to read thumbnail data", err)
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

	thumbnailFilename := fmt.Sprintf("%s.%s", videoID, filetype[1])
	thumbnailPath := filepath.Join(cfg.assetsRoot, thumbnailFilename)
	file, err := os.Create(thumbnailPath)
	defer file.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Error opening file at path %s", thumbnailPath), err)
		return
	}

	_, err = file.Write(data)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Error writing to file", thumbnailPath), err)
		return

	}

	thumbnailUrl := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, thumbnailFilename)
	videoMetaData.ThumbnailURL = &thumbnailUrl

	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video data", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetaData)
}
