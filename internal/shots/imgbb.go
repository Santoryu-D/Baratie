package shots

// Baratie 0.4.4 : hébergement sur imgbb (clé API gratuite) et vérification
// à travers wsrv.nl, le relais d'images de UNIT3D.
//
// UNIT3D ne met pas les images de la description telles quelles : il
// les fait passer par https://wsrv.nl/?n=-1&ll&url=…, sauf liste blanche.
// PixHost refuse ce relais (vignettes cassées sur le tracker) ; imgbb le laisse
// passer.

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

// ImgBBBase : l'API d'imgbb. Changée par les tests.
var ImgBBBase = "https://api.imgbb.com"

// ImgBBMax : la limite d'imgbb (32 Mo par image).
const ImgBBMax = 32 << 20

var hostClient = &http.Client{Timeout: 2 * time.Minute}

// ImgBB envoie une image et rend ses adresses : le lien mène à l'image en
// taille réelle, la vignette est la version moyenne d'imgbb.
func ImgBB(ctx context.Context, key, path string) (Hosted, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Hosted{}, errors.New("clé API imgbb manquante : ajoute-la dans les réglages (api.imgbb.com ▸ Get API key)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Hosted{}, err
	}
	if len(data) > ImgBBMax {
		return Hosted{}, errors.New("image de plus de 32 Mo : imgbb la refuserait")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	fw, _ := w.CreateFormFile("image", filepath.Base(path))
	fw.Write(data)
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ImgBBBase+"/1/upload?key="+url.QueryEscape(key), &buf)
	if err != nil {
		return Hosted{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := hostClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			// L'erreur réseau contient l'adresse, donc la clé : on ne la montre pas.
			return Hosted{}, fmt.Errorf("imgbb injoignable : %v", ue.Err)
		}
		return Hosted{}, errors.New("imgbb injoignable")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		Success bool `json:"success"`
		Data    struct {
			URL        string `json:"url"`
			DisplayURL string `json:"display_url"`
			Viewer     string `json:"url_viewer"`
			Thumb      struct {
				URL string `json:"url"`
			} `json:"thumb"`
			Medium struct {
				URL string `json:"url"`
			} `json:"medium"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &r)
	if resp.StatusCode != http.StatusOK || !r.Success {
		msg := r.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(body))
			if len(msg) > 150 {
				msg = msg[:150]
			}
		}
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(msg), "key") {
			msg = "clé API refusée (vérifie-la dans les réglages)"
		}
		return Hosted{}, fmt.Errorf("imgbb : %s (HTTP %d)", msg, resp.StatusCode)
	}
	thumb := r.Data.Medium.URL
	if thumb == "" {
		thumb = r.Data.DisplayURL
	}
	if thumb == "" {
		thumb = r.Data.Thumb.URL
	}
	if !strings.HasPrefix(r.Data.URL, "https://") || !strings.HasPrefix(thumb, "https://") {
		return Hosted{}, errors.New("imgbb : réponse inattendue")
	}
	return Hosted{Show: r.Data.URL, Thumb: thumb}, nil
}

// WsrvBase : le relais d'images de UNIT3D. Changé par les tests.
var WsrvBase = "https://wsrv.nl"

// ErrRelayDown : wsrv.nl injoignable depuis le NAS (la vérification n'a pas
// pu se faire ; ce n'est pas un refus).
var ErrRelayDown = errors.New("wsrv.nl injoignable : vérification impossible")

// CheckRelay charge l'image à travers wsrv.nl, exactement comme la page de
// UNIT3D : une image refusée ici serait cassée sur la fiche.
func CheckRelay(ctx context.Context, img string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, WsrvBase+"/?n=-1&ll&url="+url.QueryEscape(img), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Baratie)")
	resp, err := hostClient.Do(req)
	if err != nil {
		return ErrRelayDown
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		return fmt.Errorf("le relais d'images d'UNIT3D (wsrv.nl) ne l'affiche pas (HTTP %d)", resp.StatusCode)
	}
	return nil
}
