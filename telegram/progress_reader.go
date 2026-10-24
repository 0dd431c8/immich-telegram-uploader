package telegram

import "io"

type ProgressFunc func(sent, total int64)

type ProgressReader struct {
	Reader     io.Reader
	Total      int64
	ReadBytes  int64
	OnProgress ProgressFunc
}

func (p *ProgressReader) Read(b []byte) (int, error) {
	n, err := p.Reader.Read(b)

	p.ReadBytes += int64(n)

	if p.OnProgress != nil {
		p.OnProgress(p.ReadBytes, p.Total)
	}

	return n, err
}
