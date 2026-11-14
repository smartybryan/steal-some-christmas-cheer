package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/smartybryan/steal-some-christmas-cheer/internal/imaging"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/store"
)

// The Duck HQ photo is uploaded by the owner and shown at the start of the
// public trail.

func (s *Server) homePhotoURL(ctx context.Context) string {
	version, ok, err := s.store.ImageVersion(ctx, store.HomePhotoName)
	if err != nil {
		log.Printf("home photo version: %v", err)
	}
	if !ok {
		return ""
	}
	return s.cfg.PublicURL + "/api/home-photo?v=" + strconv.FormatInt(version, 36)
}

func (s *Server) handleHomePhoto(w http.ResponseWriter, r *http.Request) {
	photo, err := s.store.Image(r.Context(), store.HomePhotoName)
	// The URL changes whenever the photo does, so it can be cached for a long time.
	writePhoto(w, photo, err, "public, max-age=86400")
}

func (s *Server) handlePutHomePhoto(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "That upload was too big or garbled. Try a smaller photo.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	photo, ok := readPhoto(w, r, "Please choose a photo to upload.")
	if !ok {
		return
	}
	if err := s.store.SaveImage(r.Context(), store.HomePhotoName, photo, s.cfg.Now()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"homePhotoUrl": s.homePhotoURL(r.Context())})
}

func (s *Server) handleDeleteHomePhoto(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteImage(r.Context(), store.HomePhotoName); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"homePhotoUrl": ""})
}

// readPhoto reads the "photo" file from a parsed multipart form and
// normalizes it. On failure it writes an error response and returns false.
func readPhoto(w http.ResponseWriter, r *http.Request, missingMsg string) ([]byte, bool) {
	file, _, err := r.FormFile("photo")
	if err != nil {
		writeError(w, http.StatusBadRequest, missingMsg)
		return nil, false
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "We couldn't read that photo.")
		return nil, false
	}
	photo, err := imaging.Normalize(raw, maxPhotoSide)
	if errors.Is(err, imaging.ErrTooLarge) {
		writeError(w, http.StatusBadRequest, "That photo is enormous! Please use a smaller one.")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "We couldn't read that photo. Please use a JPEG, PNG, GIF, or WebP image.")
		return nil, false
	}
	return photo, true
}
