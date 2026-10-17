package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"github.com/0dd431c8/immich-telegram-upload/config"
)

type ImmichClient struct {
	client  http.Client
	ctx     context.Context
	apiUrl  string
	token   string
	albumId string
}

type Album struct {
	Name string `json:"albumName"`
}

func New(cfg config.Config, ctx context.Context) ImmichClient {
	client := http.Client{Timeout: 10 * time.Second}

	return ImmichClient{client, ctx, cfg.ImmichAPIURL, cfg.ImmichToken, cfg.ImmichAlbumId}
}

func (client *ImmichClient) prepareReq(method string, body io.Reader, path ...string) *http.Request {
	url, err := url.JoinPath(client.apiUrl, path...)

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

	return req
}

func (client *ImmichClient) GetAllAlbums() []string {
	req := client.prepareReq(http.MethodGet, nil, "albums")

	resp, err := client.client.Do(req)

	defer func() { _ = resp.Body.Close() }()

	if err != nil {
		panic(err)
	}

	if resp.StatusCode != http.StatusOK {
		panic(resp.StatusCode)
	}

	var result []Album

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		panic(err)
	}

	fmt.Println(result)

	return []string{}
}

func (client *ImmichClient) UploadAsset(data io.Reader, filename string) error {
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)

	go func() {
		ts := time.Now().Format(time.RFC3339)

		_ = mw.WriteField("fileCreatedAt", ts)
		_ = mw.WriteField("fileModifiedAt", ts)

		var part io.Writer

		part, _ = mw.CreateFormFile("assetData", filename)

		_, err := io.Copy(part, data)

		if err != nil {
			fmt.Println(err)
		}

		if err := mw.Close(); err != nil {
			writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
	}()

	req := client.prepareReq(http.MethodPost, reader, "assets")

	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.client.Do(req)

	if err != nil {
		return err
	}

	var result struct {
		Id string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if client.albumId != "" {
		return client.moveToAlbum(result.Id)
	}

	return nil
}

func (client *ImmichClient) moveToAlbum(assetId string) error {
	params := struct {
		AlbumIds []string `json:"albumIds"`
		AssetIds []string `json:"assetIds"`
	}{
		[]string{client.albumId},
		[]string{assetId},
	}

	body, err := json.Marshal(params)

	if err != nil {
		return err
	}

	req := client.prepareReq(http.MethodPut, bytes.NewReader(body), "albums", "assets")

	req.Header.Set("Content-Type", "application/json")

	res, err := client.client.Do(req)

	if err != nil {
		return err
	}

	if res.StatusCode > 201 {
		return errors.New("failed to move to album")
	}

	return nil
}
