package immich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"github.com/0dd431c8/immich-telegram-upload/config"
)

type ImmichClient struct {
	client http.Client
	ctx    context.Context
	apiUrl string
	token  string
}

type Album struct {
	Name string `json:"albumName"`
}

func New(cfg config.Config, ctx context.Context) ImmichClient {
	client := http.Client{Timeout: 10 * time.Second}

	return ImmichClient{client, ctx, cfg.ImmichAPIURL, cfg.ImmichToken}
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

	defer resp.Body.Close()

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

func (client *ImmichClient) UploadAsset(data io.Reader, filename string) {
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
		fmt.Println(err)
	}

	fmt.Printf("resp.Status: %v\n", resp.Status)

	var result struct {
		Id string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		panic(err)
	}

	fmt.Printf("result: %v\n", result)

	client.MoveToAlbum("eaa67a93-ade9-4c2f-a168-30146be3a048", result.Id)
}

func (client *ImmichClient) MoveToAlbum(albumId string, assetId string) {
	req := client.prepareReq(http.MethodPut, nil, "albums", "assets")

	q := req.URL.Query()
	q.Set("albumIds", albumId)
	q.Set("assetIds", assetId)

	req.URL.RawQuery = q.Encode()

	_, err := client.client.Do(req)

	if err != nil {
		fmt.Println(err)
	}
}

// func (client *ImmichClient) GetAlbum(id string) Album {
// 	req := client.prepareGetReq("albums", id)
//
// 	resp, err := client.client.Do(req)
//
// 	if err != nil {
// 		panic(err)
// 	}
// }
