package immich

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/0dd431c8/immich-telegram-upload/config"
)

func testClient(ctx context.Context, apiURL, album string) Client {
	perClientAlbum := album == ""

	return New(
		config.Config{
			ImmichAPIURL:         apiURL,
			ImmichToken:          "test-key",
			ImmichAlbumId:        album,
			ImmichPerClientAlbum: perClientAlbum,
		},
		ctx,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func TestUploadAsset(t *testing.T) {
	for _, album := range []string{"", "album-1"} {
		t.Run("album="+album, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				if r.Header.Get("x-api-key") != "test-key" {
					t.Error("missing API key")
				}
				switch r.URL.Path {
				case "/api/assets":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						http.Error(w, "invalid multipart", http.StatusBadRequest)
						return
					}
					defer r.MultipartForm.RemoveAll()
					for _, field := range []string{"fileCreatedAt", "fileModifiedAt"} {
						if _, err := time.Parse(time.RFC3339, r.FormValue(field)); err != nil {
							t.Errorf("%s: %v", field, err)
						}
					}
					file, header, err := r.FormFile("assetData")
					if err != nil {
						t.Error(err)
						return
					}
					defer file.Close()
					data, err := io.ReadAll(file)
					if err != nil || string(data) != "media content" || header.Filename != "photo.jpg" {
						t.Errorf("uploaded file = %q, %q, %v", header.Filename, data, err)
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":"asset-1"}`)
				case "/api/assets/asset-1":
					var body struct {
						Description string `json:"description"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Description != "Uploaded by @alice via telegram" {
						t.Errorf("description = %q", body.Description)
					}
				case "/api/albums/assets":
					var body struct {
						AlbumIDs []string `json:"albumIds"`
						AssetIDs []string `json:"assetIds"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					expectedAlbum := album

					if expectedAlbum == "" {
						expectedAlbum = "created-album"
					}

					if !reflect.DeepEqual(body.AlbumIDs, []string{expectedAlbum}) || !reflect.DeepEqual(body.AssetIDs, []string{"asset-1"}) {
						t.Errorf("expected %v, got %v", []string{expectedAlbum}, body)
					}
				case "/api/albums":
					switch r.Method {
					case http.MethodGet:
						_, _ = io.WriteString(w, "[]")
					case http.MethodPost:
						var body struct {
							AlbumName string `json:"albumName"`
						}

						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}

						if body.AlbumName != "@alice uploads" {
							t.Errorf("unexpected album name")
						}

						_, _ = io.WriteString(w, `{"id": "created-album", "albumName": "@alice uploads"}`)
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := testClient(context.Background(), server.URL+"/api/", album)
			if err := client.UploadAsset(strings.NewReader("media content"), "photo.jpg", "alice"); err != nil {
				t.Fatal(err)
			}
			want := []string{"POST /api/assets", "PUT /api/assets/asset-1"}

			if album == "" {
				want = append(want, "GET /api/albums")
				want = append(want, "POST /api/albums")
			}

			want = append(want, "PUT /api/albums/assets")
			if !reflect.DeepEqual(requests, want) {
				t.Errorf("requests = %v, want %v", requests, want)
			}
		})
	}
}

func TestUploadAssetFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failAt     string
		status     int
		body       string
		want       string
		requestNum int
	}{
		{"upload rejected", "/assets", 403, "upload denied", "upload denied", 1},
		{"invalid response", "/assets", 201, "not json", "invalid character", 1},
		{"description rejected", "/assets/asset-1", 500, "description denied", "failed to set description: description denied", 2},
		{"album rejected", "/albums/assets", 500, "album denied", "failed to add to album: album denied", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path == tc.failAt {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				_, _ = io.WriteString(w, `{"id":"asset-1"}`)
			}))
			defer server.Close()
			client := testClient(context.Background(), server.URL, "album-1")
			err := client.UploadAsset(strings.NewReader("media"), "photo.jpg", "alice")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
			if count != tc.requestNum {
				t.Errorf("requests = %d, want %d", count, tc.requestNum)
			}
		})
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestUploadAssetPropagatesReaderError(t *testing.T) {
	sourceErr := errors.New("media stream interrupted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"id":"asset-1"}`)
	}))
	defer server.Close()
	client := testClient(context.Background(), server.URL, "")
	if err := client.UploadAsset(failingReader{sourceErr}, "photo.jpg", "alice"); !errors.Is(err, sourceErr) {
		t.Errorf("error = %v, want source error %v", err, sourceErr)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestClientClosesResponseBodies(t *testing.T) {
	for _, operation := range []string{"upload", "description", "album"} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(operation+"/"+http.StatusText(status), func(t *testing.T) {
				var bodies []*trackedBody
				client := testClient(context.Background(), "http://immich.invalid", "")
				client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Body != nil {
						_, _ = io.Copy(io.Discard, r.Body)
						_ = r.Body.Close()
					}
					body := &trackedBody{Reader: strings.NewReader(`{"id":"asset-1"}`)}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
				})
				switch operation {
				case "upload":
					_ = client.UploadAsset(strings.NewReader("media"), "photo.jpg", "alice")
				case "description":
					_ = client.setAssetDescription("asset-1", "description")
				case "album":
					_ = client.addAssetToAlbum("asset-1", client.albumID)
				}
				if len(bodies) == 0 {
					t.Fatal("no HTTP requests made")
				}
				for i, body := range bodies {
					if !body.closed {
						t.Errorf("response body %d was not closed", i+1)
					}
				}
			})
		}
	}
}

func TestClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("canceled request reached server")
	}))
	defer server.Close()
	client := testClient(ctx, server.URL, "album-1")
	for _, operation := range []func() error{
		func() error { return client.setAssetDescription("asset-1", "description") },
		func() error { return client.addAssetToAlbum("asset-1", client.albumID) },
	} {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	}
}
