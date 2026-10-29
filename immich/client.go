package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/0dd431c8/immich-telegram-uploader/config"
)

const uploadsAlbumNameFormat = "@%s uploads"

type Client struct {
	httpClient     http.Client
	ctx            context.Context
	apiURL         string
	token          string
	albumID        string
	perClientAlbum bool
	albumsMu       *sync.Mutex
	logger         *slog.Logger
}

type albumResponseDto struct {
	ID        string `json:"id"`
	AlbumName string `json:"albumName"`
}

func New(cfg config.Config, ctx context.Context, logger *slog.Logger) Client {
	client := http.Client{Timeout: 10 * time.Second}

	return Client{client, ctx, cfg.ImmichAPIURL, cfg.ImmichToken, cfg.ImmichAlbumId, cfg.ImmichPerClientAlbum, &sync.Mutex{}, logger}
}

func (client *Client) prepareReq(method string, body io.Reader, path ...string) *http.Request {
	url, err := url.JoinPath(client.apiURL, path...)

	if err != nil {
		panic(err)
	}

	req, err := http.NewRequestWithContext(
		client.ctx,
		method,
		url,
		body,
	)

	if err != nil {
		panic(err)
	}

	req.Header.Set(
		"x-api-key",
		client.token,
	)

	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}

	return req
}

func (client *Client) UploadAsset(data io.Reader, filename string, author string) error {
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)

	defer func() { _ = reader.Close() }()

	go func() {
		defer func() {
			if err := mw.Close(); err != nil {
				writer.CloseWithError(err)
			} else {
				_ = writer.Close()
			}
		}()

		ts := time.Now().Format(time.RFC3339)

		err := mw.WriteField("fileCreatedAt", ts)

		if err != nil {
			client.logger.Error(err.Error())
			return
		}

		err = mw.WriteField("fileModifiedAt", ts)

		if err != nil {
			client.logger.Error(err.Error())
			return
		}

		var part io.Writer

		part, err = mw.CreateFormFile("assetData", filename)

		if err != nil {
			client.logger.Error(err.Error())
			return
		}

		_, err = io.Copy(part, data)

		if err != nil {
			client.logger.Error(err.Error())

			writer.CloseWithError(err)
		}
	}()

	req := client.prepareReq(http.MethodPost, reader, "assets")

	req.Header.Set("Content-Type", mw.FormDataContentType())

	res, err := client.httpClient.Do(req)

	if err != nil {
		return err
	}

	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		r, _ := io.ReadAll(res.Body)

		return errors.New(string(r))
	}

	var newAsset struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(res.Body).Decode(&newAsset); err != nil {
		return err
	}

	err = client.setAssetDescription(newAsset.ID, fmt.Sprintf("Uploaded by @%s via telegram", author))

	if err != nil {
		return err
	}

	if !client.perClientAlbum {
		return client.addAssetToAlbum(newAsset.ID, client.albumID)
	}

	client.albumsMu.Lock()
	defer client.albumsMu.Unlock()

	albums, err := client.getAllAlbums()

	if err != nil {
		return err
	}

	var album albumResponseDto

	for _, a := range albums {
		if a.AlbumName == fmt.Sprintf(uploadsAlbumNameFormat, author) {
			album = a
			break
		}
	}

	if album.ID == "" {
		album, err = client.createAlbum(fmt.Sprintf(uploadsAlbumNameFormat, author))

		if err != nil {
			return err
		}
	}

	return client.addAssetToAlbum(newAsset.ID, album.ID)
}

func (client *Client) setAssetDescription(assetID string, description string) error {
	params := struct {
		Description string `json:"description"`
	}{
		Description: description,
	}

	body, err := json.Marshal(params)

	if err != nil {
		return err
	}

	req := client.prepareReq(http.MethodPut, bytes.NewReader(body), "assets", assetID)

	res, err := client.httpClient.Do(req)

	if err != nil {
		return err
	}

	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		r, _ := io.ReadAll(res.Body)
		return errors.New("failed to set description: " + string(r))
	}

	return nil
}

func (client *Client) getAllAlbums() ([]albumResponseDto, error) {
	req := client.prepareReq(http.MethodGet, nil, "albums")

	res, err := client.httpClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("failed to load albums: %s", string(r))
	}

	var result []albumResponseDto

	err = json.NewDecoder(res.Body).Decode(&result)

	if err != nil {
		return nil, err
	}

	return result, nil
}

func (client *Client) createAlbum(albumName string) (albumResponseDto, error) {
	client.logger.Info("Creating album", "album_name", albumName)

	params := struct {
		AlbumName string `json:"albumName"`
	}{
		AlbumName: albumName,
	}

	body, _ := json.Marshal(params)

	req := client.prepareReq(http.MethodPost, bytes.NewReader(body), "albums")

	res, err := client.httpClient.Do(req)

	if err != nil {
		return albumResponseDto{}, err
	}

	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		r, _ := io.ReadAll(res.Body)

		return albumResponseDto{}, fmt.Errorf("failed to create an album: %s", string(r))
	}

	var result albumResponseDto

	err = json.NewDecoder(res.Body).Decode(&result)

	return result, err
}

func (client *Client) addAssetToAlbum(assetID string, albumID string) error {
	params := struct {
		AlbumIDs []string `json:"albumIds"`
		AssetIDs []string `json:"assetIds"`
	}{
		[]string{albumID},
		[]string{assetID},
	}

	client.logger.Info("Adding asset to album", "asset_id", assetID, "album_id", albumID)

	body, err := json.Marshal(params)

	if err != nil {
		return err
	}

	req := client.prepareReq(http.MethodPut, bytes.NewReader(body), "albums", "assets")

	res, err := client.httpClient.Do(req)

	if err != nil {
		return err
	}

	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		r, _ := io.ReadAll(res.Body)
		return errors.New("failed to add to album: " + string(r))
	}

	return nil
}
