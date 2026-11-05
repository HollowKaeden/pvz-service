package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type apiClient struct {
	base, billing string
	http          *http.Client
}
type reception struct {
	ID       string    `json:"id"`
	DateTime time.Time `json:"dateTime"`
}
type fixture struct {
	BaseURL string   `json:"base_url"`
	Query   string   `json:"query"`
	PVZIDs  []string `json:"pvz_ids"`
}
type listItem struct {
	PVZ struct {
		ID string `json:"id"`
	} `json:"pvz"`
	Receptions []struct {
		Products []json.RawMessage `json:"products"`
	} `json:"receptions"`
}

func (a apiClient) call(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	base := a.base
	if strings.HasPrefix(path, "/billing/") && a.billing != "" {
		base = a.billing
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func (a apiClient) newReception(ctx context.Context, moderator, employee string) (string, reception, error) {
	var pvz struct {
		ID string `json:"id"`
	}
	if err := a.call(ctx, http.MethodPost, "/pvz", moderator, map[string]string{"city": "Казань"}, &pvz); err != nil {
		return "", reception{}, err
	}
	var rec reception
	err := a.call(ctx, http.MethodPost, "/receptions", employee, map[string]string{"pvzId": pvz.ID}, &rec)
	return pvz.ID, rec, err
}

func (a apiClient) readFixture(ctx context.Context, dir, moderator, employee string) (fixture, error) {
	path := filepath.Join(dir, "fixture.json")
	var f fixture
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &f); err != nil {
			return f, err
		}
		if f.BaseURL != a.base {
			return f, errors.New("fixture от другого сервера: используйте отдельный -out")
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return f, err
		}
		f.BaseURL = a.base
		var first, last time.Time
		for range 10 {
			id, rec, err := a.newReception(ctx, moderator, employee)
			if err != nil {
				return f, err
			}
			f.PVZIDs = append(f.PVZIDs, id)
			if first.IsZero() || rec.DateTime.Before(first) {
				first = rec.DateTime
			}
			if rec.DateTime.After(last) {
				last = rec.DateTime
			}
			for range 50 {
				if err := a.call(ctx, http.MethodPost, "/products", employee, map[string]string{"pvzId": id, "type": "обувь"}, nil); err != nil {
					return f, err
				}
			}
			if err := a.call(ctx, http.MethodPost, "/pvz/"+id+"/close_last_reception", employee, nil, nil); err != nil {
				return f, err
			}
		}
		q := url.Values{"page": {"1"}, "limit": {"10"}, "startDate": {first.Format(time.RFC3339Nano)}, "endDate": {last.Format(time.RFC3339Nano)}}
		f.Query = "/pvz?" + q.Encode()
		if err := saveJSON(path, f); err != nil {
			return f, err
		}
	}
	var items []listItem
	if err := a.call(ctx, http.MethodGet, f.Query, employee, nil, &items); err != nil {
		return f, err
	}
	ids := make(map[string]bool, len(f.PVZIDs))
	for _, id := range f.PVZIDs {
		ids[id] = true
	}
	if len(ids) != 10 || len(items) != 10 {
		return f, errors.New("fixture изменён или сброшен: используйте новый каталог -out")
	}
	for _, item := range items {
		if !ids[item.PVZ.ID] || len(item.Receptions) != 1 || len(item.Receptions[0].Products) != 50 {
			return f, errors.New("fixture не соответствует 10 × 1 × 50: используйте новый -out")
		}
	}
	return f, nil
}
