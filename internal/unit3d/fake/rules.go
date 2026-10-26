package fake

import "baratie/internal/unit3d"

// Identifiants du faux tracker (ceux d'une fiche UNIT3D courante).
const (
	CatFilms       = 1
	CatFilmsVOSTFR = 6
	TypeFullDisc   = 1
	TypeRemux      = 2
	TypeEncode     = 3
	TypeWEB        = 4
	TypeHDTV       = 6
	TypeDVDR       = 7
	Type3D         = 8
)

// Rules : des règles d'exemple, pour les tests et la démo (teams et seuils
// fictifs).
func Rules() unit3d.Rules {
	return unit3d.Rules{
		Name:         "Faux",
		Categories:   map[string]int{"films": CatFilms, "films_vostfr": CatFilmsVOSTFR},
		Types:        map[string]int{"fulldisc": TypeFullDisc, "remux": TypeRemux, "encode": TypeEncode, "web": TypeWEB, "hdtv": TypeHDTV, "dvdr": TypeDVDR, "3d": Type3D},
		Resolutions:  map[string]int{"4320p": 1, "2160p": 2, "1080p": 3, "1080i": 4, "720p": 5, "576p": 6, "576i": 7, "480p": 8, "480i": 9, "Other": 10},
		BannedTeams:  map[string]string{"BADTEAM": "team d'exemple refusée"},
		RawOnlyTeams: []string{"BRUT"},
		BannedTokens: map[string]string{
			"HDLIGHT": "encodes allégés refusés", "4KLIGHT": "encodes allégés refusés", "UHDLIGHT": "encodes allégés refusés", "MHD": "encodes allégés refusés",
			"CAM": "source CAM refusée", "TS": "source Telesync refusée", "TELESYNC": "source Telesync refusée", "SCR": "screener refusé",
		},
		Quality: &unit3d.Quality{
			VONeedsFrenchSubs: true, MultiNeedsOriginal: true,
			MinKbps: map[string]int{
				"bluray/x264/720p": 4000, "bluray/x264/1080p": 8000, "bluray/x264/2160p": 16000,
				"bluray/x265/720p": 3000, "bluray/x265/1080p": 6000, "bluray/x265/2160p": 12000,
				"web/x264/720p": 3000, "web/x264/1080p": 5000, "web/x264/2160p": 10000,
				"web/x265/720p": 2000, "web/x265/1080p": 3500, "web/x265/2160p": 8000,
			},
			MinKbpsAnimation: map[string]int{"bluray/x264/720p": 2300, "bluray/x264/1080p": 5000, "web/x264/1080p": 3000},
			SDMinKbps:        750, SDMaxAC3Kbps: 448, HDMinAudioKbps: 384,
		},
	}
}
