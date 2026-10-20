// Package bencode lit et écrit le format des fichiers .torrent (BEP 3).
//
// Le décodeur garde, pour chaque dictionnaire, les octets bruts de la valeur
// « info » : l'info-hash se calcule sur ces octets exacts.
package bencode

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Value est une valeur décodée : int64, []byte, []Value ou Dict.
type Value interface{}

// Dict est un dictionnaire bencode (clés triées à l'encodage).
type Dict map[string]Value

// ErrSyntax signale un fichier bencode invalide.
var ErrSyntax = errors.New("bencode invalide")

// Decoded est le résultat d'un décodage : la valeur racine et, si la racine est
// un dictionnaire qui contient « info », les octets bruts de cette valeur.
type Decoded struct {
	Root    Value
	RawInfo []byte
}

// Decode lit un document bencode complet.
func Decode(data []byte) (*Decoded, error) {
	d := &decoder{data: data}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	return &Decoded{Root: v, RawInfo: d.rawInfo}, nil
}

type decoder struct {
	data    []byte
	pos     int
	rawInfo []byte
}

const maxDepth = 64

func (d *decoder) value(depth int) (Value, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w : trop d'imbrication", ErrSyntax)
	}
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("%w : fin inattendue", ErrSyntax)
	}
	switch c := d.data[d.pos]; {
	case c == 'i':
		end := bytes.IndexByte(d.data[d.pos:], 'e')
		if end < 0 {
			return nil, fmt.Errorf("%w : entier non terminé", ErrSyntax)
		}
		n, err := strconv.ParseInt(string(d.data[d.pos+1:d.pos+end]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w : entier", ErrSyntax)
		}
		d.pos += end + 1
		return n, nil
	case c == 'l':
		d.pos++
		list := []Value{}
		for {
			if d.pos >= len(d.data) {
				return nil, fmt.Errorf("%w : liste non terminée", ErrSyntax)
			}
			if d.data[d.pos] == 'e' {
				d.pos++
				return list, nil
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case c == 'd':
		d.pos++
		dict := Dict{}
		for {
			if d.pos >= len(d.data) {
				return nil, fmt.Errorf("%w : dictionnaire non terminé", ErrSyntax)
			}
			if d.data[d.pos] == 'e' {
				d.pos++
				return dict, nil
			}
			k, err := d.bytesValue()
			if err != nil {
				return nil, err
			}
			start := d.pos
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if depth == 0 && string(k) == "info" {
				d.rawInfo = d.data[start:d.pos]
			}
			dict[string(k)] = v
		}
	case c >= '0' && c <= '9':
		return d.bytesValue()
	default:
		return nil, fmt.Errorf("%w : caractère inattendu", ErrSyntax)
	}
}

func (d *decoder) bytesValue() ([]byte, error) {
	colon := bytes.IndexByte(d.data[d.pos:], ':')
	if colon < 0 {
		return nil, fmt.Errorf("%w : chaîne", ErrSyntax)
	}
	n, err := strconv.Atoi(string(d.data[d.pos : d.pos+colon]))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("%w : longueur de chaîne", ErrSyntax)
	}
	start := d.pos + colon + 1
	if start+n > len(d.data) {
		return nil, fmt.Errorf("%w : chaîne tronquée", ErrSyntax)
	}
	d.pos = start + n
	return d.data[start : start+n], nil
}

// Encode écrit une valeur en bencode (clés de dictionnaire triées, BEP 3).
func Encode(v Value) ([]byte, error) {
	var b bytes.Buffer
	if err := encode(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encode(b *bytes.Buffer, v Value) error {
	switch x := v.(type) {
	case int64:
		fmt.Fprintf(b, "i%de", x)
	case int:
		fmt.Fprintf(b, "i%de", x)
	case []byte:
		fmt.Fprintf(b, "%d:", len(x))
		b.Write(x)
	case string:
		fmt.Fprintf(b, "%d:%s", len(x), x)
	case []Value:
		b.WriteByte('l')
		for _, e := range x {
			if err := encode(b, e); err != nil {
				return err
			}
		}
		b.WriteByte('e')
	case Dict:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('d')
		for _, k := range keys {
			fmt.Fprintf(b, "%d:%s", len(k), k)
			if err := encode(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('e')
	default:
		return fmt.Errorf("bencode : type non pris en charge %T", v)
	}
	return nil
}
