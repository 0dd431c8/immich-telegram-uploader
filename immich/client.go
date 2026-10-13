package immich

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/0dd431c8/immich-telegram-upload/app"
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

func New(cfg app.Config, ctx context.Context) ImmichClient {
	client := http.Client{Timeout: 10 * time.Second}

	return ImmichClient{client, ctx, cfg.ImmichAPIURL, cfg.ImmichToken}
}

func (client *ImmichClient) prepareGetReq(path ...string) *http.Request {
	url, err := url.JoinPath(client.apiUrl, path...)

	if err != nil {
		panic(err)
	}

	req, err := http.NewRequestWithContext(
		client.ctx,
		http.MethodGet,
		url,
		nil,
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
	req := client.prepareGetReq("albums")

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

func (client *ImmichClient) GetAlbum(id string) Album {
	req := client.prepareGetReq("albums", id)

	resp, err := client.client.Do(req)

	if err != nil {
		panic(err)
	}
}
