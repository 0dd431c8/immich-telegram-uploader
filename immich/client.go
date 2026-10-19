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
	"time"

	"github.com/0dd431c8/immich-telegram-upload/config"
)

type Client struct {
	httpClient http.Client
	ctx        context.Context
	apiUrl     string
	token      string
	albumId    string
	logger     *slog.Logger
}

type Album struct {
	Name string `json:"albumName"`
}

func New(cfg config.Config, ctx context.Context, logger *slog.Logger) Client {
	client := http.Client{Timeout: 10 * time.Second}

	return Client{client, ctx, cfg.ImmichAPIURL, cfg.ImmichToken, cfg.ImmichAlbumId, logger}
}

func (client *Client) prepareReq(method string, body io.Reader, path ...string) *http.Request {
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

	req.Header.Set("Content-Type", "application/json")

	return req
}

func (client *Client) UploadAsset(data io.Reader, filename string, author string) error {
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)

	defer reader.Close()

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

	resp, err := client.httpClient.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode > 201 {
		r, _ := io.ReadAll(resp.Body)

		return errors.New(string(r))
	}

	var result struct {
		Id string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	err = client.setAssetDescription(result.Id, fmt.Sprintf("Uploaded by @%s via telegram", author))

	if err != nil {
		return err
	}

	if client.albumId != "" {
		return client.addAssetToAlbum(result.Id)
	}

	return nil
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

	defer res.Body.Close()

	if res.StatusCode > 201 {
		r, _ := io.ReadAll(res.Body)
		return errors.New("failed to set description: " + string(r))
	}

	return nil
}

func (client *Client) addAssetToAlbum(assetId string) error {
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

	res, err := client.httpClient.Do(req)

	if err != nil {
		return err
	}

	defer res.Body.Close()

	if res.StatusCode > 201 {
		r, _ := io.ReadAll(res.Body)
		return errors.New("failed to add to album: " + string(r))
	}

	return nil
}
