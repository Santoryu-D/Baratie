package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// withPermHint complète un refus d'écriture par ce qu'il faut faire. Sous
// Docker, un dossier de la machine monté dans le conteneur garde son
// propriétaire, alors que Baratie tourne sous un autre utilisateur (exemple :
// « open /config/config.json.tmp: permission denied »).
func withPermHint(err error) error {
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if os.Getenv("BARATIE_DOCKER") != "" {
		return fmt.Errorf("%w. Baratie tourne sous l'utilisateur %d:%d et ce dossier appartient à un autre : ajoute au service baratie la ligne user: \"UID:GID\" avec les numéros du propriétaire du dossier (la commande id les donne), puis docker compose up -d", err, os.Getuid(), os.Getgid())
	}
	return fmt.Errorf("%w. Baratie n'a pas le droit d'écrire dans ce dossier : vérifie ses droits, ou choisis un dossier à toi", err)
}
