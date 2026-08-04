package publishapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/store"
)

func testAPI(t *testing.T, token string) (*API, *store.Store) {
	t.Helper()
	root := t.TempDir()
	cfg := config.New(root, filepath.Join(root, "output"))
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(st, cfg, TokenHash(token)), st
}

func authorizedRequest(method, target, token string, body *bytes.Buffer) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestTokenRequired(t *testing.T) {
	api, _ := testAPI(t, "correct-token")
	for _, token := range []string{"", "wrong-token"} {
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, authorizedRequest(http.MethodGet, "/", token, &bytes.Buffer{}))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status = %d, want 401", token, rec.Code)
		}
	}

	api.SetTokenHash("")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status = %d, want 503", rec.Code)
	}
}

func TestGenerateToken(t *testing.T) {
	first, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second || TokenHash(first) == first {
		t.Fatalf("generated tokens are not independent opaque secrets")
	}
}

func TestBuildTrigger(t *testing.T) {
	const token = "correct-token"
	api, _ := testAPI(t, token)
	triggered := false
	api.SetBuildTrigger(func() error {
		triggered = true
		return nil
	})
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, authorizedRequest(http.MethodPost, "/sync/build", token, &bytes.Buffer{}))
	if rec.Code != http.StatusAccepted || !triggered {
		t.Fatalf("build status = %d, triggered = %v", rec.Code, triggered)
	}
}

func TestCreateListAndUpload(t *testing.T) {
	const token = "correct-token"
	api, st := testAPI(t, token)
	handler := api.Handler()

	createBody := bytes.NewBufferString(`{"title":"Lightroom Collection","status":"draft"}`)
	req := authorizedRequest(http.MethodPost, "/galleries", token, createBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("create response = %q, err = %v", rec.Body.String(), err)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authorizedRequest(http.MethodGet, "/galleries", token, &bytes.Buffer{}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"Lightroom Collection"`) {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "export.jpg")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := range 6 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: 40, G: 80, B: 120, A: 255})
		}
	}
	if err := jpeg.Encode(part, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("caption", "Published from Lightroom"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req = authorizedRequest(http.MethodPost, "/galleries/"+strconv.FormatInt(created.ID, 10)+"/photos", token, &upload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	items, err := st.ItemsByGallery(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Caption != "Published from Lightroom" {
		t.Fatalf("items = %#v", items)
	}
}

func TestSyncGalleryHierarchyIsIdempotent(t *testing.T) {
	const token = "correct-token"
	api, st := testAPI(t, token)
	handler := api.Handler()

	put := func(externalID, body string, wantStatus int) int64 {
		t.Helper()
		req := authorizedRequest(http.MethodPut, "/sync/galleries/"+externalID, token, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("sync %s status = %d, body = %s", externalID, rec.Code, rec.Body.String())
		}
		var response struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.ID == 0 {
			t.Fatalf("sync %s response = %q, err = %v", externalID, rec.Body.String(), err)
		}
		return response.ID
	}

	parentID := put("set-1", `{"title":"Trips","status":"draft"}`, http.StatusCreated)
	childID := put("collection-2", `{"title":"Norway","parent_external_id":"set-1","status":"draft"}`, http.StatusCreated)
	if err := st.UpdateGalleryStatus(context.Background(), childID, model.GalleryPublished); err != nil {
		t.Fatal(err)
	}
	if got := put("collection-2", `{"title":"Norway 2026","parent_external_id":"set-1","status":"draft"}`, http.StatusOK); got != childID {
		t.Fatalf("repeated sync id = %d, want %d", got, childID)
	}
	child, err := st.Gallery(context.Background(), childID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Title != "Norway 2026" || child.Status != model.GalleryPublished || child.ParentID == nil || *child.ParentID != parentID {
		t.Fatalf("synchronized child = %#v", child)
	}
}

func TestSyncGalleryUsesConfiguredDefaults(t *testing.T) {
	const token = "correct-token"
	api, st := testAPI(t, token)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "site.default_gallery_published", "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.default_gallery_show_exif", "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.base_url", "https://photos.example.com"); err != nil {
		t.Fatal(err)
	}

	req := authorizedRequest(http.MethodPut, "/sync/galleries/source:collection-1", token, bytes.NewBufferString(`{"title":"Visible now"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("sync status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	gallery, err := st.Gallery(ctx, response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gallery.Status != model.GalleryPublished || gallery.ShowEXIF != model.VisibilityInherit {
		t.Fatalf("sync defaults not applied: %#v", gallery)
	}
	defaults, err := st.GalleryPresentationDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !gallery.ShowEXIF.Resolve(defaults.ShowEXIF) {
		t.Fatalf("sync EXIF default not resolved: %#v", defaults)
	}
	if response.URL != "https://photos.example.com/visible-now/" {
		t.Fatalf("public URL = %q", response.URL)
	}
	var publishedAt string
	if err := st.DB.QueryRowContext(ctx, `SELECT published_at FROM galleries WHERE id = ?`, response.ID).Scan(&publishedAt); err != nil || publishedAt == "" {
		t.Fatalf("published_at = %q, err = %v", publishedAt, err)
	}
}

func TestPublicGalleryURLRequiresAddressableAncestors(t *testing.T) {
	_, st := testAPI(t, "correct-token")
	ctx := context.Background()
	if err := st.SetSetting(ctx, "site.base_url", "https://photos.example.com"); err != nil {
		t.Fatal(err)
	}
	parentID, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "draft-parent", Title: "Draft parent", Type: model.GalleryGrid, Status: model.GalleryDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	childID, err := st.CreateGallery(ctx, model.Gallery{
		ParentID: &parentID, Slug: "published-child", Title: "Published child",
		Type: model.GalleryGrid, Status: model.GalleryPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	api := New(st, config.New(t.TempDir(), t.TempDir()), TokenHash("correct-token"))
	if publicURL, err := api.publicGalleryURL(ctx, childID); err != nil || publicURL != "" {
		t.Fatalf("public URL = %q, err = %v", publicURL, err)
	}
}

func TestSyncPhotoReplacesExistingItem(t *testing.T) {
	const token = "correct-token"
	api, st := testAPI(t, token)
	handler := api.Handler()
	ctx := context.Background()
	if err := st.SetSetting(ctx, "site.default_gallery_published", "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.base_url", "https://photos.example.com"); err != nil {
		t.Fatal(err)
	}

	galleryRequest := authorizedRequest(http.MethodPut, "/sync/galleries/collection-1", token, bytes.NewBufferString(`{"title":"Synced"}`))
	galleryRequest.Header.Set("Content-Type", "application/json")
	galleryResponse := httptest.NewRecorder()
	handler.ServeHTTP(galleryResponse, galleryRequest)
	var gallery struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(galleryResponse.Body.Bytes(), &gallery); err != nil {
		t.Fatal(err)
	}

	upload := func(externalID, caption, lens string) (int, int64, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "photo.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if err := jpeg.Encode(part, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("caption", caption); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("lens", lens); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		target := "/sync/galleries/" + strconv.FormatInt(gallery.ID, 10) + "/photos/" + externalID
		req := authorizedRequest(http.MethodPost, target, token, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var response struct {
			ID  int64  `json:"id"`
			URL string `json:"url"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("response = %q, err = %v", rec.Body.String(), err)
		}
		return rec.Code, response.ID, response.URL
	}

	firstStatus, firstID, firstURL := upload("collection-1:photo-9", "First", "Voigtlander 40mm f/1.2")
	secondStatus, secondID, _ := upload("collection-1:photo-9", "Updated", "")
	if firstStatus != http.StatusCreated || secondStatus != http.StatusOK || firstID != secondID {
		t.Fatalf("statuses = %d, %d; ids = %d, %d", firstStatus, secondStatus, firstID, secondID)
	}
	if firstURL != "https://photos.example.com/synced/#photo-photo" {
		t.Fatalf("photo URL = %q", firstURL)
	}
	items, err := st.ItemsByGallery(context.Background(), gallery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Caption != "Updated" || items[0].Filename != "photo.jpg" || items[0].LightroomLens != "" {
		t.Fatalf("synchronized items = %#v", items)
	}
	_, thirdID, _ := upload("collection-1:photo-10", "Another photo with the same filename", "Voigtlander 40mm f/1.2")
	items, err = st.ItemsByGallery(context.Background(), gallery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || thirdID == firstID || items[0].OriginalPath == items[1].OriginalPath {
		t.Fatalf("duplicate-filename items = %#v", items)
	}
	if items[1].LightroomLens != "Voigtlander 40mm f/1.2" || items[1].Lens != "Voigtlander 40mm f/1.2" {
		t.Fatalf("Lightroom lens not applied: %#v", items[1])
	}
}

func TestSyncOrderAndDeletionRequireOwnership(t *testing.T) {
	const token = "correct-token"
	api, st := testAPI(t, token)
	ctx := context.Background()
	galleryID, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "owned", Title: "Owned", Type: model.GalleryGrid,
		Status: model.GalleryDraft, SortMode: model.SortDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetExternalGallery(ctx, "lightroom", "collection-1", galleryID); err != nil {
		t.Fatal(err)
	}
	firstID, err := st.CreateItem(ctx, model.Item{GalleryID: galleryID, OriginalPath: "owned/first.jpg", Filename: "first.jpg", Status: model.ItemPublished})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := st.CreateItem(ctx, model.Item{GalleryID: galleryID, OriginalPath: "owned/second.jpg", Filename: "second.jpg", Status: model.ItemPublished})
	if err != nil {
		t.Fatal(err)
	}
	manualID, err := st.CreateItem(ctx, model.Item{GalleryID: galleryID, OriginalPath: "owned/manual.jpg", Filename: "manual.jpg", Status: model.ItemPublished})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetExternalItem(ctx, "lightroom", "first", firstID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetExternalItem(ctx, "lightroom", "second", secondID); err != nil {
		t.Fatal(err)
	}

	orderBody := bytes.NewBufferString(fmt.Sprintf(`{"item_ids":[%d,%d]}`, secondID, firstID))
	orderRequest := authorizedRequest(http.MethodPut, fmt.Sprintf("/sync/galleries/%d/order", galleryID), token, orderBody)
	orderRequest.Header.Set("Content-Type", "application/json")
	orderResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(orderResponse, orderRequest)
	if orderResponse.Code != http.StatusNoContent {
		t.Fatalf("order status = %d, body = %s", orderResponse.Code, orderResponse.Body.String())
	}
	items, err := st.ItemsByGallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ID != secondID || items[1].ID != firstID || items[2].ID != manualID {
		t.Fatalf("ordered items = %#v", items)
	}

	deleteRequest := authorizedRequest(http.MethodDelete, fmt.Sprintf("/sync/photos/%d", firstID), token, &bytes.Buffer{})
	deleteResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("owned delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	manualRequest := authorizedRequest(http.MethodDelete, fmt.Sprintf("/sync/photos/%d", manualID), token, &bytes.Buffer{})
	manualResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(manualResponse, manualRequest)
	if manualResponse.Code != http.StatusNotFound {
		t.Fatalf("manual delete status = %d, want 404", manualResponse.Code)
	}

	galleryDeleteRequest := authorizedRequest(http.MethodDelete, fmt.Sprintf("/sync/galleries/%d", galleryID), token, &bytes.Buffer{})
	galleryDeleteResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(galleryDeleteResponse, galleryDeleteRequest)
	if galleryDeleteResponse.Code != http.StatusConflict {
		t.Fatalf("gallery delete with manual item status = %d, want 409", galleryDeleteResponse.Code)
	}
	if err := st.DeleteItem(ctx, manualID); err != nil {
		t.Fatal(err)
	}
	galleryDeleteResponse = httptest.NewRecorder()
	api.Handler().ServeHTTP(galleryDeleteResponse, galleryDeleteRequest)
	if galleryDeleteResponse.Code != http.StatusNoContent {
		t.Fatalf("owned gallery delete status = %d, body = %s", galleryDeleteResponse.Code, galleryDeleteResponse.Body.String())
	}
}
