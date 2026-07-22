package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

const (
	// maxDocumentFileBytes caps the size of a single uploaded document.
	maxDocumentFileBytes = 10 << 20 // 10 MiB
	// maxDocumentUploadBytes caps the whole multipart upload request, allowing
	// several files to be attached in one go.
	maxDocumentUploadBytes = 64 << 20 // 64 MiB
	// documentFormField is the multipart field carrying uploaded files.
	documentFormField = "file"
)

// inlineDocTypes are the media types that are safe to render inline in the
// browser. Everything else is served as a download (Content-Disposition:
// attachment) so uploaded HTML/SVG/etc. can never execute in this origin.
var inlineDocTypes = map[string]bool{
	"application/pdf": true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/webp":      true,
}

// attachmentsView is the data for the reusable attachments fragment (the list of
// documents plus the upload control) shown on an item or a travel leg.
type attachmentsView struct {
	ListURL string // base URL for this owner's documents (GET list / POST upload)
	CSRF    string
	Docs    []documentView
	Error   string
}

// documentView is one document chip in the attachments list.
type documentView struct {
	ID       string
	Filename string
	Icon     string
	Href     string // open/download URL
	Preview  string // "pdf", "image", or "" when the type is download-only
}

func newDocumentView(d models.Document) documentView {
	icon := "📄"
	if d.IsImage() {
		icon = "🖼"
	}
	return documentView{
		ID:       d.ID.String(),
		Filename: d.Filename,
		Icon:     icon,
		Href:     "/documents/" + d.ID.String(),
		Preview:  previewKind(d.ContentType),
	}
}

// docMediaType returns the base media type (without parameters) of a stored
// content type, falling back to the raw value.
func docMediaType(contentType string) string {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		return mt
	}
	return contentType
}

// previewKind reports how a document can be shown in the in-page preview modal:
// "pdf" (iframe), "image" (img), or "" for types that are download-only.
func previewKind(contentType string) string {
	mt := docMediaType(contentType)
	if !inlineDocTypes[mt] {
		return ""
	}
	if mt == "application/pdf" {
		return "pdf"
	}
	return "image"
}

func toDocumentViews(docs []models.Document) []documentView {
	out := make([]documentView, 0, len(docs))
	for _, d := range docs {
		out = append(out, newDocumentView(d))
	}
	return out
}

// ---- item documents ----

func (s *Server) handleItemDocuments(w http.ResponseWriter, r *http.Request) {
	itemID, ok := s.itemForDocuments(w, r)
	if !ok {
		return
	}
	s.renderItemAttachments(w, r, itemID, "")
}

func (s *Server) handleUploadItemDocument(w http.ResponseWriter, r *http.Request) {
	itemID, ok := s.itemForDocuments(w, r)
	if !ok {
		return
	}
	loc := i18n.FromContext(r.Context())
	err := s.saveUploadedDocuments(r, loc, func(d *models.Document) {
		id := itemID
		d.ItemID = &id
	})
	msg := ""
	if err != nil {
		if verr := validationMessage(err); verr != "" {
			msg = verr
		} else {
			s.serverError(w, r, err)
			return
		}
	}
	s.renderItemAttachments(w, r, itemID, msg)
}

// itemForDocuments resolves and validates the item id from the URL.
func (s *Server) itemForDocuments(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	itemID, err := urlUUID(r, "itemID")
	if err != nil {
		s.notFound(w, r)
		return uuid.Nil, false
	}
	if _, err := s.store.GetItem(r.Context(), itemID); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return uuid.Nil, false
		}
		s.serverError(w, r, err)
		return uuid.Nil, false
	}
	return itemID, true
}

func (s *Server) renderItemAttachments(w http.ResponseWriter, r *http.Request, itemID uuid.UUID, errMsg string) {
	docs, err := s.store.ListItemDocuments(r.Context(), itemID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.fragment(w, r, "attachments", attachmentsView{
		ListURL: "/items/" + itemID.String() + "/documents",
		CSRF:    csrfToken(r.Context()),
		Docs:    toDocumentViews(docs),
		Error:   errMsg,
	})
}

// ---- travel documents ----

func (s *Server) handleTravelDocuments(w http.ResponseWriter, r *http.Request) {
	vacationID, kind, step, ok := s.travelForDocuments(w, r)
	if !ok {
		return
	}
	s.renderTravelAttachments(w, r, vacationID, kind, step, "")
}

func (s *Server) handleUploadTravelDocument(w http.ResponseWriter, r *http.Request) {
	vacationID, kind, step, ok := s.travelForDocuments(w, r)
	if !ok {
		return
	}
	loc := i18n.FromContext(r.Context())
	err := s.saveUploadedDocuments(r, loc, func(d *models.Document) {
		id := vacationID
		d.VacationID = &id
		d.TravelKind = kind
		d.TravelStep = step
	})
	msg := ""
	if err != nil {
		if verr := validationMessage(err); verr != "" {
			msg = verr
		} else {
			s.serverError(w, r, err)
			return
		}
	}
	s.renderTravelAttachments(w, r, vacationID, kind, step, msg)
}

// travelForDocuments resolves and validates the vacation id, travel kind and
// step order from the URL.
func (s *Server) travelForDocuments(w http.ResponseWriter, r *http.Request) (uuid.UUID, models.TravelKind, int, bool) {
	vacationID, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return uuid.Nil, "", 0, false
	}
	kind := models.TravelKind(chi.URLParam(r, "kind"))
	if !kind.Valid() {
		s.notFound(w, r)
		return uuid.Nil, "", 0, false
	}
	step, err := strconv.Atoi(chi.URLParam(r, "step"))
	if err != nil || step < 0 {
		s.notFound(w, r)
		return uuid.Nil, "", 0, false
	}
	if _, err := s.store.GetVacation(r.Context(), vacationID); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return uuid.Nil, "", 0, false
		}
		s.serverError(w, r, err)
		return uuid.Nil, "", 0, false
	}
	return vacationID, kind, step, true
}

func (s *Server) renderTravelAttachments(w http.ResponseWriter, r *http.Request, vacationID uuid.UUID, kind models.TravelKind, step int, errMsg string) {
	docs, err := s.store.ListTravelDocuments(r.Context(), vacationID, kind, step)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.fragment(w, r, "attachments", attachmentsView{
		ListURL: fmt.Sprintf("/vacations/%s/traveldocs/%s/%d", vacationID, kind, step),
		CSRF:    csrfToken(r.Context()),
		Docs:    toDocumentViews(docs),
		Error:   errMsg,
	})
}

// ---- serve & delete ----

func (s *Server) handleServeDocument(w http.ResponseWriter, r *http.Request) {
	docID, err := urlUUID(r, "docID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	doc, err := s.store.ReadDocument(r.Context(), docID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	disposition := "attachment"
	if inlineDocTypes[docMediaType(doc.ContentType)] {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", doc.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"%s; filename=\"%s\"; filename*=UTF-8''%s",
		disposition, asciiFilename(doc.Filename), rfc5987Escape(doc.Filename)))
	// Reaffirm the global nosniff header; user-supplied bytes must never be sniffed.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if disposition == "inline" {
		// Permit the in-page preview modal to embed the file in a same-origin
		// iframe/img; the global headers otherwise forbid all framing.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
	}
	http.ServeContent(w, r, doc.Filename, doc.CreatedAt, bytes.NewReader(doc.Data))
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	docID, err := urlUUID(r, "docID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	doc, err := s.store.GetDocument(r.Context(), docID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	if err := s.store.DeleteDocument(r.Context(), docID); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	switch {
	case doc.ItemID != nil:
		s.renderItemAttachments(w, r, *doc.ItemID, "")
	case doc.VacationID != nil:
		s.renderTravelAttachments(w, r, *doc.VacationID, doc.TravelKind, doc.TravelStep, "")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- upload helper ----

// saveUploadedDocuments parses the multipart request and stores each uploaded
// file, calling assign to attach it to its owner. A per-file size violation (or
// an empty upload) is returned as a validation error.
func (s *Server) saveUploadedDocuments(r *http.Request, loc *i18n.Localizer, assign func(*models.Document)) error {
	// The request body is bounded by the bodyLimit middleware (MaxBytesReader),
	// so multipart parsing cannot exhaust memory here.
	if err := r.ParseMultipartForm(32 << 20); err != nil { //nolint:gosec // G120: body is capped by the bodyLimit middleware
		if errors.Is(err, http.ErrNotMultipart) {
			return errValidation(loc.T("error.doc_none"))
		}
		return err
	}
	if r.MultipartForm == nil {
		return errValidation(loc.T("error.doc_none"))
	}
	files := r.MultipartForm.File[documentFormField]
	if len(files) == 0 {
		return errValidation(loc.T("error.doc_none"))
	}

	saved := 0
	for _, fh := range files {
		if fh.Size <= 0 {
			continue
		}
		if fh.Size > maxDocumentFileBytes {
			return errValidation(loc.T("error.doc_too_large"))
		}
		data, err := readMultipartFile(fh)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		if int64(len(data)) > maxDocumentFileBytes {
			return errValidation(loc.T("error.doc_too_large"))
		}

		doc := &models.Document{
			Filename:    cleanFilename(fh.Filename),
			ContentType: http.DetectContentType(data),
			Size:        int64(len(data)),
			Data:        data,
		}
		assign(doc)
		if err := s.store.CreateDocument(r.Context(), doc); err != nil {
			return err
		}
		saved++
	}
	if saved == 0 {
		return errValidation(loc.T("error.doc_none"))
	}
	return nil
}

func readMultipartFile(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// Read one byte past the limit so an oversized part can be detected even when
	// the reported header size was wrong.
	return io.ReadAll(io.LimitReader(f, maxDocumentFileBytes+1))
}

// ---- filename sanitation ----

// cleanFilename strips any path and control characters from an uploaded file
// name and caps its length, returning a safe display/storage name.
func cleanFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "document"
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}

// asciiFilename produces an ASCII-only, quote-safe value for the legacy
// Content-Disposition filename parameter.
func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\' || r < 0x20 || r == 0x7f:
			// drop unsafe characters
		case r > 0x7f:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "document"
	}
	return b.String()
}

// rfc5987Escape percent-encodes a UTF-8 filename for the Content-Disposition
// filename* parameter (RFC 5987).
func rfc5987Escape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
