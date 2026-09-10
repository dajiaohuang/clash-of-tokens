package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
)

const envelopeMagic = "COTK1"
const keyIDSize = 16

// Each vault uses one random OS-keychain key. The public identifier and format
// version are authenticated along with the ciphertext. Keys never enter config.
type keyEnvelope struct {
	get func(string) ([]byte, error)
	put func(string, []byte) error
	id  []byte
}

func (p *keyEnvelope) seal(plain []byte) ([]byte, error) {
	var key []byte
	var err error
	if p.id == nil {
		id := make([]byte, keyIDSize)
		key = make([]byte, 32)
		defer clear(key)
		if _, err = rand.Read(id); err != nil {
			return nil, errors.New("cannot generate vault identifier")
		}
		if _, err = rand.Read(key); err != nil {
			return nil, errors.New("cannot generate vault key")
		}
		if err = p.put(hex.EncodeToString(id), key); err != nil {
			return nil, errors.New("cannot store vault key in OS keychain")
		}
		p.id = id
	} else {
		key, err = p.get(hex.EncodeToString(p.id))
		if err != nil || len(key) != 32 {
			clear(key)
			return nil, errors.New("cannot read vault key from OS keychain")
		}
		defer clear(key)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid vault key")
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errors.New("cannot initialize vault encryption")
	}
	header := append([]byte(envelopeMagic), p.id...)
	return aead.Seal(header, nil, plain, header), nil
}

func (p *keyEnvelope) unseal(data []byte) ([]byte, error) {
	const headerSize = len(envelopeMagic) + keyIDSize
	if len(data) < headerSize+28 || string(data[:len(envelopeMagic)]) != envelopeMagic {
		return nil, errors.New("unsupported credential vault format")
	}
	id := data[len(envelopeMagic):headerSize]
	key, err := p.get(hex.EncodeToString(id))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, errors.New("cannot read vault key from OS keychain")
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid vault key")
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errors.New("cannot initialize vault encryption")
	}
	plain, err := aead.Open(nil, nil, data[headerSize:], data[:headerSize])
	if err != nil {
		return nil, errors.New("cannot authenticate credential vault")
	}
	p.id = append([]byte(nil), id...)
	return plain, nil
}
