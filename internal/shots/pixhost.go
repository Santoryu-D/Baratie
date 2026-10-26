package shots

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
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PixHostBase : l'API de PixHost (v2, sans compte ni clé). Changée par les tests.
var PixHostBase = "https://api.pixhost.to"

// ThumbSize : largeur maximale des vignettes (150 à 500).
const ThumbSize = 350

var pixClient = &http.Client{Timeout: 2 * time.Minute}

// PixHost envoie une image (contenu tout public) et rend ses adresses.
func PixHost(ctx context.Context, path string) (Hosted, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Hosted{}, err
	}
	if len(data) > MaxBytes {
		return Hosted{}, errors.New("image de plus de 10 Mo : PixHost la refuserait")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("content_type", "0")
	_ = w.WriteField("max_th_size", fmt.Sprint(ThumbSize))
	fw, _ := w.CreateFormFile("img", filepath.Base(path))
	fw.Write(data)
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, PixHostBase+"/images", &buf)
	if err != nil {
		return Hosted{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := pixClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return Hosted{}, fmt.Errorf("PixHost injoignable : %v", ue.Err)
		}
		return Hosted{}, errors.New("PixHost injoignable")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := map[int]string{400: "image manquante", 413: "image trop lourde", 414: "format refusé", 422: "image refusée"}[resp.StatusCode]
		if msg == "" {
			msg = strings.TrimSpace(string(body))
			if len(msg) > 150 {
				msg = msg[:150]
			}
		}
		return Hosted{}, fmt.Errorf("PixHost : %s (HTTP %d)", msg, resp.StatusCode)
	}
	var r struct {
		ShowURL string `json:"show_url"`
		ThURL   string `json:"th_url"`
	}
	if err := json.Unmarshal(body, &r); err != nil || !strings.HasPrefix(r.ShowURL, "https://") || !strings.HasPrefix(r.ThURL, "https://") {
		return Hosted{}, errors.New("PixHost : réponse inattendue")
	}
	return Hosted{Show: r.ShowURL, Thumb: r.ThURL}, nil
}
