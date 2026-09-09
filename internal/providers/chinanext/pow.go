package chinanext

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const maxPowDifficulty = 250000

type powChallenge struct {
	Algorithm  string `json:"algorithm"`
	Challenge  string `json:"challenge"`
	Salt       string `json:"salt"`
	Signature  string `json:"signature"`
	Difficulty int    `json:"difficulty"`
	ExpireAt   int64  `json:"expire_at"`
	TargetPath string `json:"target_path"`
}

func encodePowAnswer(ch powChallenge, answer int) string {
	b, _ := json.Marshal(map[string]any{
		"algorithm": ch.Algorithm, "challenge": ch.Challenge, "salt": ch.Salt,
		"answer": answer, "signature": ch.Signature, "target_path": ch.TargetPath,
	})
	return base64.StdEncoding.EncodeToString(b)
}

// DeepSeekHashV1 is SHA3-256 with the final Keccak-p round omitted. The web
// challenge compares the digest of salt_expiry_nonce with its challenge hash.
func deepSeekHashV1(input []byte) [32]byte {
	const rate = 136
	var state [25]uint64
	for len(input) >= rate {
		for i := 0; i < rate/8; i++ {
			state[i] ^= littleEndian64(input[i*8:])
		}
		keccakP23(&state)
		input = input[rate:]
	}
	var block [rate]byte
	copy(block[:], input)
	block[len(input)] ^= 0x06
	block[rate-1] ^= 0x80
	for i := 0; i < rate/8; i++ {
		state[i] ^= littleEndian64(block[i*8:])
	}
	keccakP23(&state)
	var out [32]byte
	for i := 0; i < len(out); i++ {
		out[i] = byte(state[i/8] >> (8 * (i % 8)))
	}
	return out
}

func littleEndian64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

var powRotation = [25]uint{0, 1, 62, 28, 27, 36, 44, 6, 55, 20, 3, 10, 43, 25, 39, 41, 45, 15, 21, 8, 18, 2, 61, 56, 14}
var powRoundConstants = [24]uint64{
	0x0000000000000001, 0x0000000000008082, 0x800000000000808a, 0x8000000080008000,
	0x000000000000808b, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
	0x000000000000008a, 0x0000000000000088, 0x0000000080008009, 0x000000008000000a,
	0x000000008000808b, 0x800000000000008b, 0x8000000000008089, 0x8000000000008003,
	0x8000000000008002, 0x8000000000000080, 0x000000000000800a, 0x800000008000000a,
	0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008,
}

func rotate64(v uint64, n uint) uint64 {
	if n == 0 {
		return v
	}
	return (v << n) | (v >> (64 - n))
}

func keccakP23(a *[25]uint64) {
	var c, d [5]uint64
	var b [25]uint64
	for round := 1; round < 24; round++ {
		for x := 0; x < 5; x++ {
			c[x] = a[x] ^ a[x+5] ^ a[x+10] ^ a[x+15] ^ a[x+20]
		}
		for x := 0; x < 5; x++ {
			d[x] = c[(x+4)%5] ^ rotate64(c[(x+1)%5], 1)
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				a[x+5*y] ^= d[x]
			}
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				b[y+5*((2*x+3*y)%5)] = rotate64(a[x+5*y], powRotation[x+5*y])
			}
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				a[x+5*y] = b[x+5*y] ^ (^b[(x+1)%5+5*y] & b[(x+2)%5+5*y])
			}
		}
		a[0] ^= powRoundConstants[round]
	}
}

const deepSeekCompletionPath = "/api/v0/chat/completion"

func solvePow(ctx context.Context, ch powChallenge) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ch.Algorithm != "DeepSeekHashV1" || len(ch.Challenge) != 64 {
		return -1, errors.New("china next web adapter: invalid DeepSeek PoW challenge")
	}
	if ch.Difficulty < 1 || ch.Difficulty > maxPowDifficulty || ch.Salt == "" || len(ch.Salt) > 1024 || ch.ExpireAt <= time.Now().UnixMilli() {
		return -1, errors.New("china next web adapter: DeepSeek PoW challenge is outside limits")
	}
	if ch.TargetPath != deepSeekCompletionPath {
		return -1, fmt.Errorf("china next web adapter: DeepSeek PoW target path %q is unsupported", ch.TargetPath)
	}
	challenge, e := hex.DecodeString(ch.Challenge)
	if e != nil || len(challenge) != 32 {
		return -1, errors.New("china next web adapter: DeepSeek PoW challenge is not hexadecimal")
	}
	prefix := ch.Salt + "_" + strconv.FormatInt(ch.ExpireAt, 10) + "_"
	for nonce := 0; nonce < ch.Difficulty; nonce++ {
		if nonce&0xFF == 0 {
			select {
			case <-ctx.Done():
				return -1, ctx.Err()
			default:
			}
		}
		candidate := make([]byte, len(prefix), len(prefix)+20)
		copy(candidate, prefix)
		candidate = strconv.AppendInt(candidate, int64(nonce), 10)
		digest := deepSeekHashV1(candidate)
		if bytes.Equal(digest[:], challenge) {
			return nonce, nil
		}
	}
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	default:
	}
	return -1, errors.New("china next web adapter: DeepSeek PoW nonce not found")
}
