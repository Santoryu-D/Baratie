package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// withPermHint complète un refus d'écriture par ce qu'il faut faire. Sous
// Docker, un dossier de la machine monté dans le conteneur garde son
// propriétaire, alors que D3X tourne sous un autre utilisateur (Ekhstaz,
// 23/10/26 : « open /config/D3X/config.json.tmp: permission denied »).
func withPermHint(err error) error {
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if os.Getenv("D3X_MANAGED") == "docker" {
		return fmt.Errorf("%w. D3X tourne sous l'utilisateur %d:%d et ce dossier appartient à un autre : ajoute au service d3x la ligne user: \"UID:GID\" avec les numéros du propriétaire du dossier (la commande id les donne), puis docker compose up -d", err, os.Getuid(), os.Getgid())
	}
	return fmt.Errorf("%w. D3X n'a pas le droit d'écrire dans ce dossier : vérifie ses droits, ou choisis un dossier à toi", err)
}
