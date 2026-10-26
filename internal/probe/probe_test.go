package probe

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseJSON(t *testing.T) {
	js := `{"streams":[
	 {"codec_type":"video","codec_name":"hevc","width":3840,"height":1608,"pix_fmt":"yuv420p10le","color_transfer":"smpte2084","tags":{"BPS-eng":"18500000","NUMBER_OF_BYTES-eng":"14000000000"},"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8}]},
	 {"codec_type":"audio","codec_name":"truehd","channels":8,"tags":{"language":"eng","BPS-eng":"4000000"},"disposition":{"default":1}},
	 {"codec_type":"audio","codec_name":"eac3","channels":6,"bit_rate":"640000","tags":{"language":"fre"}},
	 {"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"fre"},"disposition":{"forced":1}},
	 {"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],
	 "format":{"format_name":"matroska,webm","duration":"6300.0","bit_rate":"23000000"}}`
	mi, err := Parse([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if mi.Container != "Matroska" || mi.VideoFmt != "HEVC" || mi.BitDepth != 10 || mi.VideoKbps != 18500 || mi.Width != 3840 {
		t.Fatalf("vidéo : %+v", mi)
	}
	if mi.HDR != "Dolby Vision, SMPTE ST 2084" || !mi.HasFrenchAudio() || !mi.HasFrenchSubs() || mi.Audios[0].Format != "MLP FBA" || mi.Audios[1].Kbps != 640 || mi.Subs[0].Format != "PGS" || !mi.Subs[0].Forced {
		t.Fatalf("pistes : %+v", mi)
	}
	if mi.Resolution() != "2160p" {
		t.Fatal(mi.Resolution())
	}
}

func TestFile(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err != nil || err2 != nil {
		t.Skip("ffmpeg absent")
	}
	p := filepath.Join(t.TempDir(), "Film.2001.FRENCH.DVDRip.XviD-GRP.avi")
	cmd := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=720x304:rate=25:duration=3", "-f", "lavfi", "-i", "sine=duration=3",
		"-c:v", "mpeg4", "-vtag", "XVID", "-b:v", "1200k", "-c:a", "ac3", "-b:a", "448k", "-metadata:s:a:0", "language=fre", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("vidéo de test : %s", out)
	}
	mi, err := File(context.Background(), fp, p)
	if err != nil {
		t.Fatal(err)
	}
	if mi.Container != "AVI" || mi.VideoFmt != "MPEG-4 Visual" || mi.VideoCodec != "XVID" || len(mi.Audios) != 1 || mi.Audios[0].Format != "AC-3" || mi.Audios[0].Kbps != 448 {
		t.Fatalf("%+v", mi)
	}
}
