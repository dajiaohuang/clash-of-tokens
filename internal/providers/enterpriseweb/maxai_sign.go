package enterpriseweb

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"time"
)

var maxAIHex40 = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var maxAIAppVersion = regexp.MustCompile(`^webpage_[0-9]+\.[0-9]+\.[0-9]+$`)

func validMaxAICredential(c credentialJSON) bool {
	return c.AccessToken != "" && c.DeviceID != "" && c.UserID != "" && c.HMACKey != "" && c.AESKey != "" && maxAIHex40.MatchString(c.ContextKey) && maxAIAppVersion.MatchString(c.AppVersion)
}

func maxAIProof(path string, reqTime int64, userID, key, appVersion string) string {
	sign := appVersion + ":" + strconv.FormatInt(reqTime, 10) + ":" + path + ":" + userID
	h := hmac.New(sha1.New, []byte(strconv.FormatInt(reqTime, 10)+":"+key))
	_, _ = h.Write([]byte(sign))
	sha := hex.EncodeToString(h.Sum(nil))
	return sm3Hex(strconv.FormatInt(reqTime, 10) + ":" + sha + ":" + key)
}

func maxAIRandom() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "100000"
	}
	n := 100000 + int((uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]))%900000)
	return strconv.Itoa(n)
}

func maxAIAuthorization(c credentialJSON, path string) (string, error) {
	now := timeNowMilli()
	// The key order is part of the browser signer payload.
	var p bytes.Buffer
	p.WriteString(`{"X-Client-Domain":"maxai.co","X-Client-Path":"https://www.maxai.co/app/","X-Random":`)
	p.WriteString(strconv.Quote(maxAIRandom()))
	p.WriteString(`,"t":`)
	p.WriteString(strconv.FormatInt(now, 10))
	p.WriteString(`,"p":`)
	p.WriteString(strconv.Quote(maxAIProof("/gpt/cwc/chat", now, c.UserID, c.HMACKey, c.AppVersion)))
	p.WriteString(`,"d":`)
	p.WriteString(strconv.Quote(c.DeviceID))
	p.WriteString(`,`)
	p.WriteString(strconv.Quote(c.ContextKey))
	p.WriteString(`:{"a":""}}`)
	return maxAIAESEncrypt(p.Bytes(), c.AESKey)
}

var timeNowMilli = func() int64 { return time.Now().UnixMilli() }

func maxAIAESEncrypt(plaintext []byte, passphrase string) (string, error) {
	if passphrase == "" {
		return "", errors.New("enterpriseweb adapter: MaxAI signing key missing")
	}
	var salt [8]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	key, iv := evpBytesToKey([]byte(passphrase), salt[:], 32, 16)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	plaintext = append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plaintext)
	blob := append([]byte("Salted__"), salt[:]...)
	blob = append(blob, out...)
	return base64.StdEncoding.EncodeToString(blob), nil
}

func evpBytesToKey(passphrase, salt []byte, keyLen, ivLen int) ([]byte, []byte) {
	derived := make([]byte, 0, keyLen+ivLen)
	prev := []byte{}
	for len(derived) < keyLen+ivLen {
		h := md5.New()
		_, _ = h.Write(prev)
		_, _ = h.Write(passphrase)
		_, _ = h.Write(salt)
		prev = h.Sum(nil)
		derived = append(derived, prev...)
	}
	return derived[:keyLen], derived[keyLen : keyLen+ivLen]
}

// SM3 is the hash used by the MaxAI browser signer. It is kept local because
// the Go standard library does not expose SM3 and no third-party crypto module
// is needed for this one deterministic primitive.
func sm3Hex(s string) string { return hex.EncodeToString(sm3([]byte(s))) }

func sm3(data []byte) []byte {
	iv := [8]uint32{0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e}
	bitLen := uint64(len(data)) * 8
	n := ((len(data) + 1 + 8 + 63) / 64) * 64
	msg := make([]byte, n)
	copy(msg, data)
	msg[len(data)] = 0x80
	for i := 0; i < 8; i++ {
		msg[n-1-i] = byte(bitLen >> (8 * i))
	}
	for off := 0; off < n; off += 64 {
		var w [68]uint32
		var w1 [64]uint32
		for i := 0; i < 16; i++ {
			w[i] = uint32(msg[off+4*i])<<24 | uint32(msg[off+4*i+1])<<16 | uint32(msg[off+4*i+2])<<8 | uint32(msg[off+4*i+3])
		}
		for j := 16; j < 68; j++ {
			w[j] = p1(w[j-16]^w[j-9]^rol(w[j-3], 15)) ^ rol(w[j-13], 7) ^ w[j-6]
		}
		for j := 0; j < 64; j++ {
			w1[j] = w[j] ^ w[j+4]
		}
		a, b, c, d, e, f, g, h := iv[0], iv[1], iv[2], iv[3], iv[4], iv[5], iv[6], iv[7]
		for j := 0; j < 64; j++ {
			tj := uint32(0x79cc4519)
			if j >= 16 {
				tj = 0x7a879d8a
			}
			ss1 := rol(rol(a, 12)+e+rol(tj, uint(j)), 7)
			ss2 := ss1 ^ rol(a, 12)
			var ff, gg uint32
			if j < 16 {
				ff = a ^ b ^ c
				gg = e ^ f ^ g
			} else {
				ff = (a & b) | (a & c) | (b & c)
				gg = (e & f) | (^e & g)
			}
			tt1 := ff + d + ss2 + w1[j]
			tt2 := gg + h + ss1 + w[j]
			d = c
			c = rol(b, 9)
			b = a
			a = tt1
			h = g
			g = rol(f, 19)
			f = e
			e = p0(tt2)
		}
		iv[0] ^= a
		iv[1] ^= b
		iv[2] ^= c
		iv[3] ^= d
		iv[4] ^= e
		iv[5] ^= f
		iv[6] ^= g
		iv[7] ^= h
	}
	out := make([]byte, 32)
	for i, v := range iv {
		out[4*i] = byte(v >> 24)
		out[4*i+1] = byte(v >> 16)
		out[4*i+2] = byte(v >> 8)
		out[4*i+3] = byte(v)
	}
	return out
}
func rol(x uint32, n uint) uint32 {
	n &= 31
	if n == 0 {
		return x
	}
	return x<<n | x>>(32-n)
}
func p0(x uint32) uint32 { return x ^ rol(x, 9) ^ rol(x, 17) }
func p1(x uint32) uint32 { return x ^ rol(x, 15) ^ rol(x, 23) }
