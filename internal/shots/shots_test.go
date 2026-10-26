package shots

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// film : une vraie petite vidéo (anamorphique : 720×576, SAR 64:45 → 1024×576
// à l'affichage) avec une piste de sous-titres, qui ne doit pas apparaître.
func film(t *testing.T) (Tool, string) {
	tool, err := Find()
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "Film.2020.FRENCH.576p.DVDRip.x264-GRP.mkv")
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:30,000\nSOUS-TITRE\n"), 0o644)
	cmd := exec.Command(tool.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=720x576:rate=25:duration=20", "-i", srt,
		"-vf", "setsar=64/45", "-c:v", "libx264", "-preset", "ultrafast", "-c:s", "srt", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("vidéo de test impossible : %s", out)
	}
	return tool, p
}

func TestMakeAndRedo(t *testing.T) {
	tool, p := film(t)
	before, _ := os.Stat(p)
	dir := filepath.Join(t.TempDir(), "captures-x")
	s, err := tool.Make(context.Background(), p, dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	// La durée annoncée peut dépasser l'image (sous-titres plus longs, MKV
	// sans durée par piste) : une capture tombée après la fin est reprise
	// plus tôt, jamais vide.
	if len(s.Shots) != 4 || s.Dur < 19 {
		t.Fatalf("%+v", s)
	}
	for i, sh := range s.Shots {
		if sh.Error != "" || sh.Size == 0 {
			t.Fatalf("capture %d : %+v", i, sh)
		}
		if filepath.Dir(sh.Path) != dir {
			t.Fatal("capture hors du dossier de travail")
		}
	}
	// DAR : 1024×576 (et non 720×576).
	out, _ := exec.Command(tool.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", s.Shots[0].Path).Output()
	if strings.TrimSpace(string(out)) != "1024,576" {
		t.Fatalf("dimensions %q, attendu 1024,576", out)
	}
	old := s.Shots[1].Path
	if err := tool.Redo(context.Background(), s, 1); err != nil || s.Shots[1].Path == old || s.Shots[1].Error != "" {
		t.Fatalf("refaire : %v %+v", err, s.Shots[1])
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("ancienne capture laissée")
	}
	// Le film n'a pas bougé.
	after, _ := os.Stat(p)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("film modifié")
	}
	s.Clean()
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("dossier de travail laissé")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("film retiré par Clean")
	}
}

func TestPixHostAndBBCode(t *testing.T) {
	var got struct {
		ct, th, name string
		size         int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images" || r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		r.ParseMultipartForm(16 << 20)
		got.ct, got.th = r.FormValue("content_type"), r.FormValue("max_th_size")
		f, h, _ := r.FormFile("img")
		b, _ := io.ReadAll(f)
		got.name, got.size = h.Filename, len(b)
		json.NewEncoder(w).Encode(map[string]interface{}{"name": h.Filename, "show_url": "https://pixhost.to/show/1/" + h.Filename, "th_url": "https://t0.pixhost.to/thumbs/1/" + h.Filename, "thumb_width": 350})
	}))
	defer srv.Close()
	old := PixHostBase
	PixHostBase = srv.URL
	defer func() { PixHostBase = old }()

	p := filepath.Join(t.TempDir(), "capture-1.png")
	os.WriteFile(p, []byte("\x89PNG fausse"), 0o600)
	h, err := PixHost(context.Background(), p)
	if err != nil || got.ct != "0" || got.th != "350" || got.size == 0 || !strings.HasPrefix(h.Thumb, "https://t0.pixhost.to/") {
		t.Fatalf("%+v %+v %v", h, got, err)
	}
	bb := BBCode([]Hosted{{"https://a/1", "https://t/1"}, {"https://a/2", "https://t/2"}, {"https://a/3", "https://t/3"}, {"https://a/4", "https://t/4"}})
	want := "[center][url=https://a/1][img]https://t/1[/img][/url] [url=https://a/2][img]https://t/2[/img][/url]\n[url=https://a/3][img]https://t/3[/img][/url] [url=https://a/4][img]https://t/4[/img][/url][/center]"
	if bb != want {
		t.Fatalf("BBCode :\n%s", bb)
	}
}

func TestPositions(t *testing.T) {
	p := Positions(100, 4)
	if len(p) != 4 || p[0] != 15 || p[3] != 75 {
		t.Fatalf("%v", p)
	}
}

// hdr4k : une vidéo 4K étiquetée HDR10 (PQ, BT.2020), en 10 bits si possible.
func hdr4k(t *testing.T, tool Tool) string {
	p := filepath.Join(t.TempDir(), "Film.2020.MULTi.2160p.UHD.BluRay.REMUX.HDR.HEVC-GRP.mkv")
	base := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=3840x2160:rate=24:duration=6"}
	tags := []string{"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc"}
	for _, enc := range [][]string{
		{"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=error"},
		{"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le"},
		{"-c:v", "libx264", "-preset", "ultrafast"},
	} {
		args := append(append(append([]string{}, base...), enc...), tags...)
		if exec.Command(tool.FFmpeg, append(args, "-y", p)...).Run() == nil {
			return p
		}
	}
	t.Skip("vidéo 4K de test impossible")
	return ""
}

func TestHDR4K(t *testing.T) {
	tool, err := Find()
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	p := hdr4k(t, tool)
	before, _ := os.Stat(p)
	v, err := tool.Probe(context.Background(), p)
	if err != nil || v.Range != HDR10 || v.Width != 3840 {
		t.Fatalf("%+v %v", v, err)
	}
	s, err := tool.Make(context.Background(), p, filepath.Join(t.TempDir(), "captures-h"), 3)
	if err != nil {
		t.Fatal(err)
	}
	zs := tool.HasZscale(context.Background())
	if s.Tonemap != zs || (zs && s.Warn != "") || (!zs && s.Warn == "") || len(s.Notes) == 0 {
		t.Fatalf("notes %v warn %q tonemap %v (zscale %v)", s.Notes, s.Warn, s.Tonemap, zs)
	}
	for _, sh := range s.Shots {
		if sh.Error != "" {
			t.Fatal(sh.Error)
		}
		out, _ := exec.Command(tool.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,pix_fmt", "-of", "csv=p=0", sh.Path).Output()
		if strings.TrimSpace(string(out)) != "1920,1080,rgb24" {
			t.Fatalf("capture %q, attendu 1920,1080,rgb24", out)
		}
	}
	after, _ := os.Stat(p)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("film modifié")
	}
	t.Logf("notes : %v ; tailles : %d %d %d", s.Notes, s.Shots[0].Size, s.Shots[1].Size, s.Shots[2].Size)
}

func TestRangeOf(t *testing.T) {
	j := func(trc string, prof, compat int) []byte {
		side := ""
		if prof != 0 {
			side = `,"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":` + strconv.Itoa(prof) + `,"dv_bl_signal_compatibility_id":` + strconv.Itoa(compat) + `}]`
		}
		return []byte(`{"streams":[{"width":3840,"height":2160,"sample_aspect_ratio":"1:1","color_transfer":"` + trc + `"` + side + `}]}`)
	}
	for _, c := range []struct {
		b    []byte
		want string
	}{
		{j("bt709", 0, 0), SDR},
		{j("", 0, 0), SDR},
		{j("smpte2084", 0, 0), HDR10},
		{j("arib-std-b67", 0, 0), HLG},
		{j("smpte2084", 7, 6), HDR10},  // Blu-ray DV profil 7
		{j("smpte2084", 8, 1), HDR10},  // DV 8.1
		{j("", 8, 1), HDR10},           // DV 8.1 sans étiquette
		{j("arib-std-b67", 8, 4), HLG}, // DV 8.4
		{j("", 5, 0), DVOnly},          // DV profil 5
		{j("unknown", 5, 0), DVOnly},
	} {
		v, err := parseProbe(c.b)
		if err != nil || v.Range != c.want {
			t.Fatalf("%s : %q, attendu %q (%v)", c.b, v.Range, c.want, err)
		}
	}
	v, _ := parseProbe([]byte(`{"streams":[{"width":720,"height":576,"sample_aspect_ratio":"64:45"}]}`))
	if w, h := v.Target(MaxWidth); w != 1024 || h != 576 {
		t.Fatalf("DVD %d×%d", w, h)
	}
	v, _ = parseProbe([]byte(`{"streams":[{"width":3840,"height":1608,"sample_aspect_ratio":"1:1"}]}`))
	if w, h := v.Target(MaxWidth); w != 1920 || h != 804 {
		t.Fatalf("scope 4K %d×%d", w, h)
	}
}

func TestImgBBAndRelay(t *testing.T) {
	var gotKey, gotName string
	ibb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/upload" || r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		gotKey = r.URL.Query().Get("key")
		if gotKey != "bonne" {
			w.WriteHeader(400)
			w.Write([]byte(`{"status_code":400,"error":{"message":"Invalid API v1 key.","code":100},"status_txt":"Bad Request"}`))
			return
		}
		r.ParseMultipartForm(16 << 20)
		_, h, _ := r.FormFile("image")
		gotName = h.Filename
		w.Write([]byte(`{"data":{"url_viewer":"https://ibb.co/x","url":"https://i.ibb.co/x/full.png","display_url":"https://i.ibb.co/y/med.png","thumb":{"url":"https://i.ibb.co/z/th.png"},"medium":{"url":"https://i.ibb.co/y/med.png"}},"success":true,"status":200}`))
	}))
	defer ibb.Close()
	old := ImgBBBase
	ImgBBBase = ibb.URL
	defer func() { ImgBBBase = old }()
	p := filepath.Join(t.TempDir(), "capture-1.png")
	os.WriteFile(p, []byte("\x89PNG fausse"), 0o600)

	if _, err := ImgBB(context.Background(), "", p); err == nil || !strings.Contains(err.Error(), "réglages") {
		t.Fatalf("clé vide : %v", err)
	}
	if _, err := ImgBB(context.Background(), "mauvaise", p); err == nil || !strings.Contains(err.Error(), "clé API refusée") {
		t.Fatalf("mauvaise clé : %v", err)
	}
	h, err := ImgBB(context.Background(), "bonne", p)
	if err != nil || h.Show != "https://i.ibb.co/x/full.png" || h.Thumb != "https://i.ibb.co/y/med.png" || gotName != "capture-1.png" {
		t.Fatalf("%+v %v %q", h, err, gotName)
	}

	// Le relais : comme UNIT3D, il refuse PixHost et laisse passer imgbb.
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("n") != "-1" || !r.URL.Query().Has("ll") {
			w.WriteHeader(400)
			return
		}
		if strings.Contains(r.URL.Query().Get("url"), "pixhost") {
			w.WriteHeader(404)
			w.Write([]byte("Error 404: Server returned 403"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG"))
	}))
	oldW := WsrvBase
	WsrvBase = relay.URL
	defer func() { WsrvBase = oldW }()
	if err := CheckRelay(context.Background(), h.Thumb); err != nil {
		t.Fatal(err)
	}
	if err := CheckRelay(context.Background(), "https://t0.pixhost.to/thumbs/1/a.png"); err == nil || err == ErrRelayDown {
		t.Fatalf("PixHost : %v", err)
	}
	relay.Close()
	if err := CheckRelay(context.Background(), h.Thumb); err != ErrRelayDown {
		t.Fatalf("relais coupé : %v", err)
	}
}
