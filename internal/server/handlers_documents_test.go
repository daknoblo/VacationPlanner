package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/applog"
	"github.com/daknoblo/vacationplanner/internal/config"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

// newIntegrationServer builds a Server backed by a real (temp) SQLite store and
// the full middleware/route stack, for end-to-end handler tests.
func newIntegrationServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.NewSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(st.Close)

	log, logs := applog.New("test")
	cfg := &config.Config{
		Env:             "test",
		CSRFKey:         []byte("0123456789abcdef0123456789abcdef"),
		MaxRequestBytes: 1 << 20,
		RequestTimeout:  10 * time.Second,
	}
	srv, err := New(cfg, log, logs, st, ai.New(""))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func uploadDoc(t *testing.T, srv *Server, url, token, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func serveDoc(srv *Server, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/documents/"+id, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestItemDocumentUploadServeDelete(t *testing.T) {
	srv := newIntegrationServer(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "Trip", Destination: "Lisbon", StartDate: time.Now().UTC(), EndDate: time.Now().UTC().Add(48 * time.Hour)}
	if err := srv.store.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	it := &models.Item{VacationID: v.ID, Title: "Ferry"}
	if err := srv.store.CreateItem(ctx, it); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	token := srv.newCSRFToken()
	uploadURL := "/items/" + it.ID.String() + "/documents"

	// Upload a PDF; the unicode filename should survive.
	pdf := []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF")
	rec := uploadDoc(t, srv, uploadURL, token, "fährticket.pdf", pdf)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fährticket.pdf") {
		t.Fatalf("upload response missing filename: %s", rec.Body.String())
	}

	docs, err := srv.store.ListItemDocuments(ctx, it.ID)
	if err != nil || len(docs) != 1 {
		t.Fatalf("ListItemDocuments: err=%v n=%d", err, len(docs))
	}
	doc := docs[0]
	if doc.ContentType != "application/pdf" {
		t.Fatalf("stored content type = %q", doc.ContentType)
	}

	// A PDF is served inline with the original bytes.
	sr := serveDoc(srv, doc.ID.String())
	if sr.Code != http.StatusOK {
		t.Fatalf("serve status = %d", sr.Code)
	}
	if ct := sr.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("serve content-type = %q", ct)
	}
	if cd := sr.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Fatalf("expected inline disposition, got %q", cd)
	}
	if !bytes.Equal(sr.Body.Bytes(), pdf) {
		t.Fatal("served bytes differ from uploaded bytes")
	}
	// Inline documents may be embedded in the same-origin preview modal.
	if xf := sr.Header().Get("X-Frame-Options"); xf != "SAMEORIGIN" {
		t.Fatalf("inline X-Frame-Options = %q, want SAMEORIGIN", xf)
	}
	if csp := sr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("inline CSP = %q, want frame-ancestors 'self'", csp)
	}

	// An unknown/binary type is served as an attachment (never inline).
	if rec := uploadDoc(t, srv, uploadURL, token, "data.bin", []byte{0, 1, 2, 3, 4, 5, 6, 7}); rec.Code != http.StatusOK {
		t.Fatalf("binary upload status = %d body=%s", rec.Code, rec.Body.String())
	}
	docs, _ = srv.store.ListItemDocuments(ctx, it.ID)
	var binID string
	for _, d := range docs {
		if d.Filename == "data.bin" {
			binID = d.ID.String()
		}
	}
	if binID == "" {
		t.Fatal("binary document not stored")
	}
	sr = serveDoc(srv, binID)
	if cd := sr.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("expected attachment disposition for binary, got %q", cd)
	}
	// Download-only responses keep the strict global framing ban.
	if xf := sr.Header().Get("X-Frame-Options"); xf != "DENY" {
		t.Fatalf("download X-Frame-Options = %q, want DENY", xf)
	}

	// Delete the PDF via the shared document route.
	dreq := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/documents/"+doc.ID.String(), nil)
	dreq.Header.Set("X-CSRF-Token", token)
	dreq.Header.Set("HX-Request", "true")
	drec := httptest.NewRecorder()
	srv.ServeHTTP(drec, dreq)
	if drec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", drec.Code, drec.Body.String())
	}
	if _, err := srv.store.GetDocument(ctx, doc.ID); err == nil {
		t.Fatal("expected document to be deleted")
	}

	// An upload without a CSRF token must be rejected.
	if rec := uploadDoc(t, srv, uploadURL, "", "x.pdf", pdf); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", rec.Code)
	}
}

func TestTravelDocumentUploadPerLeg(t *testing.T) {
	srv := newIntegrationServer(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "Trip", Destination: "Oslo", StartDate: time.Now().UTC(), EndDate: time.Now().UTC().Add(48 * time.Hour)}
	if err := srv.store.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	token := srv.newCSRFToken()

	// Upload to arrival leg 0 even though no travel segment row exists yet.
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	url := "/vacations/" + v.ID.String() + "/traveldocs/arrival/0"
	if rec := uploadDoc(t, srv, url, token, "boarding.png", png); rec.Code != http.StatusOK {
		t.Fatalf("travel upload status = %d body=%s", rec.Code, rec.Body.String())
	}

	docs, err := srv.store.ListTravelDocuments(ctx, v.ID, models.TravelArrival, 0)
	if err != nil || len(docs) != 1 {
		t.Fatalf("ListTravelDocuments: err=%v n=%d", err, len(docs))
	}
	if docs[0].ContentType != "image/png" || !docs[0].IsImage() {
		t.Fatalf("stored travel doc content type = %q", docs[0].ContentType)
	}

	// An invalid travel kind is rejected.
	if rec := uploadDoc(t, srv, "/vacations/"+v.ID.String()+"/traveldocs/bogus/0", token, "x.png", png); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for invalid kind, got %d", rec.Code)
	}
}
