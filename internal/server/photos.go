package server

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif" // decoders for uploads and thumbnails
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// Contact photos live inside the vCard (that is what phones sync). The web
// app shows square thumbnails made on the fly and kept in a small cache.

const (
	photoMaxSide = 512      // stored pictures are at most this wide or tall
	photoMaxBody = 20 << 20 // uploads
)

var thumbCache = struct {
	sync.Mutex
	m map[string][]byte
}{m: map[string][]byte{}}

// thumbnail crops the middle square of an image and scales it to size×size JPEG.
func thumbnail(data []byte, size int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	if side <= 0 {
		return nil, errors.New("empty image")
	}
	at := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	crop := image.Rectangle{Min: at, Max: at.Add(image.Pt(side, side))}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// shrinkPhoto turns an upload into what is stored: a JPEG at most 512 px on its longer side.
func shrinkPhoto(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty image")
	}
	if w > photoMaxSide || h > photoMaxSide {
		if w >= h {
			w, h = photoMaxSide, max(1, h*photoMaxSide/w)
		} else {
			w, h = max(1, w*photoMaxSide/h), photoMaxSide
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (s *Server) handleGetPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, _, _, err := s.loadContact(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	data, mime, _ := card.Photo()
	if len(data) == 0 {
		writeErr(w, http.StatusNotFound, "this contact has no picture")
		return
	}
	size := 256
	if v := r.URL.Query().Get("s"); v != "" {
		size, _ = strconv.Atoi(v)
		if size != 0 {
			size = min(max(size, 32), photoMaxSide)
		}
	}
	etag := `"` + e.PhotoHash + "-" + strconv.Itoa(size) + `"`
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, ctype := data, mime
	if size > 0 {
		key := e.PhotoHash + "-" + strconv.Itoa(size)
		thumbCache.Lock()
		cached := thumbCache.m[key]
		thumbCache.Unlock()
		if cached == nil {
			// a picture Go can't read (HEIC) is sent as it is
			if t, err := thumbnail(data, size); err == nil {
				cached = t
				thumbCache.Lock()
				if len(thumbCache.m) > 2000 {
					thumbCache.m = map[string][]byte{}
				}
				thumbCache.m[key] = t
				thumbCache.Unlock()
			}
		}
		if cached != nil {
			body, ctype = cached, "image/jpeg"
		}
	}
	if ctype == "" {
		ctype = "image/jpeg"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// readImageUpload reads a raw image body or the multipart field "file".
func readImageUpload(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(photoMaxBody); err != nil {
			return nil, badRequest("the picture is too large")
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, badRequest("send the picture as the field \"file\"")
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, photoMaxBody))
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, photoMaxBody))
	if err != nil {
		return nil, badRequest("the picture is too large")
	}
	return data, nil
}

func (s *Server) handlePutPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	e, ab, err := s.writableContact(ctx, a, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	data, err := readImageUpload(r)
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	small, err := shrinkPhoto(data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "that picture can't be read; use a JPEG, PNG, GIF or WebP")
		return
	}
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	card.SetPhoto(small, "image/jpeg")
	if _, err := s.saveCard(ctx, a, e, ab, card, e.ETag); err != nil {
		s.fail(w, err, "photo")
		return
	}
	s.logContact(ctx, a, ab, e.ID, "updated", e.FN, "picture")
	s.respondContact(w, r, http.StatusOK, e.ID)
}

func (s *Server) handleDeletePhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	e, ab, err := s.writableContact(ctx, a, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		s.fail(w, err, "photo")
		return
	}
	if card.Get("PHOTO") != nil {
		card.SetPhoto(nil, "")
		if _, err := s.saveCard(ctx, a, e, ab, card, e.ETag); err != nil {
			s.fail(w, err, "photo")
			return
		}
		s.logContact(ctx, a, ab, e.ID, "updated", e.FN, "picture removed")
	}
	s.respondContact(w, r, http.StatusOK, e.ID)
}
