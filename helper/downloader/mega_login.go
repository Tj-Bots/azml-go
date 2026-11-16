package downloader

import (
	"context"
	"crypto/aes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"

	"azml-go/config"
)

// MEGA account login (MEGA_EMAIL / MEGA_PASSWORD): with a session id the transfer quota is the
// account's, not the server IP's (on Heroku the IP is shared with many apps).

var (
	megaSessMu   sync.Mutex
	megaSID      string
	megaSIDFor   string // email:password the sid belongs to
	megaLoginErr error
)

func megaB64Enc(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func aesECBEncrypt(key, data []byte) []byte {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	out := make([]byte, len(data))
	for i := 0; i+16 <= len(data); i += 16 {
		blk.Encrypt(out[i:i+16], data[i:i+16])
	}
	return out
}

// strToA32Bytes pads to 4 bytes (Mega's str_to_a32 as bytes).
func strToA32Bytes(s string) []byte {
	b := []byte(s)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// v1 accounts: prepare_key + stringhash.
func megaPrepareKey(password []byte) []byte {
	pkey := wordsBytes([]uint32{0x93C467E3, 0x7DB0C7A4, 0xD1BE3F81, 0x0152CB56})
	pw := strToA32Bytes(string(password))
	for r := 0; r < 0x10000; r++ {
		for j := 0; j < len(pw); j += 16 {
			key := make([]byte, 16)
			copy(key, pw[j:min(j+16, len(pw))])
			pkey = aesECBEncrypt(key, pkey)
		}
	}
	return pkey
}

func megaStringHash(s string, key []byte) string {
	sw := words(strToA32Bytes(s))
	h := make([]uint32, 4)
	for i, v := range sw {
		h[i%4] ^= v
	}
	hb := wordsBytes(h)
	for r := 0; r < 0x4000; r++ {
		hb = aesECBEncrypt(key, hb)
	}
	w := words(hb)
	return megaB64Enc(wordsBytes([]uint32{w[0], w[2]}))
}

// readMPI reads one Mega MPI (2 byte bit length + number).
func readMPI(b []byte) (*big.Int, []byte, error) {
	if len(b) < 2 {
		return nil, nil, fmt.Errorf("short mpi")
	}
	n := (int(binary.BigEndian.Uint16(b)) + 7) / 8
	if len(b) < 2+n {
		return nil, nil, fmt.Errorf("short mpi")
	}
	return new(big.Int).SetBytes(b[2 : 2+n]), b[2+n:], nil
}

// megaLogin returns a session id for the account.
func megaLogin(ctx context.Context, email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var pre struct {
		V int    `json:"v"`
		S string `json:"s"`
	}
	if err := megaAPI(ctx, "", map[string]interface{}{"a": "us0", "user": email}, &pre); err != nil {
		return "", fmt.Errorf("MEGA login: %w", err)
	}
	var pk []byte
	var uh string
	if pre.V == 2 {
		salt, err := megaB64(pre.S)
		if err != nil {
			return "", fmt.Errorf("MEGA login: bad salt")
		}
		dk, err := pbkdf2.Key(sha512.New, password, salt, 100000, 32)
		if err != nil {
			return "", err
		}
		pk, uh = dk[:16], megaB64Enc(dk[16:32])
	} else {
		pk = megaPrepareKey([]byte(password))
		uh = megaStringHash(email, pk)
	}
	var res struct {
		K     string `json:"k"`
		Csid  string `json:"csid"`
		Privk string `json:"privk"`
		Tsid  string `json:"tsid"`
	}
	if err := megaAPI(ctx, "", map[string]interface{}{"a": "us", "user": email, "uh": uh}, &res); err != nil {
		if e, ok := err.(megaAPIError); ok && (int(e) == -9 || int(e) == -2) {
			return "", fmt.Errorf("MEGA login: wrong email or password")
		}
		if e, ok := err.(megaAPIError); ok && int(e) == -26 {
			return "", fmt.Errorf("MEGA login: the account has 2FA, it is not supported")
		}
		return "", fmt.Errorf("MEGA login: %w", err)
	}
	encMaster, err := megaB64(res.K)
	if err != nil {
		return "", fmt.Errorf("MEGA login: bad key")
	}
	master := ecbDecrypt(pk, encMaster)
	if res.Tsid != "" {
		return res.Tsid, nil
	}
	encPriv, err := megaB64(res.Privk)
	if err != nil {
		return "", fmt.Errorf("MEGA login: bad private key")
	}
	priv := ecbDecrypt(master, encPriv)
	p, rest, err := readMPI(priv)
	if err != nil {
		return "", err
	}
	q, rest, err := readMPI(rest)
	if err != nil {
		return "", err
	}
	d, _, err := readMPI(rest)
	if err != nil {
		return "", err
	}
	csid, err := megaB64(res.Csid)
	if err != nil {
		return "", fmt.Errorf("MEGA login: bad csid")
	}
	c, _, err := readMPI(csid)
	if err != nil {
		return "", err
	}
	m := new(big.Int).Exp(c, d, new(big.Int).Mul(p, q)).Bytes()
	if len(m) < 43 {
		return "", fmt.Errorf("MEGA login: bad session")
	}
	return megaB64Enc(m[:43]), nil
}

// megaSession: the logged-in session id ("" without MEGA_EMAIL/MEGA_PASSWORD or on failure).
func megaSession(ctx context.Context) string {
	cfg := config.Get()
	if cfg.MegaEmail == "" || cfg.MegaPassword == "" {
		return ""
	}
	key := cfg.MegaEmail + ":" + cfg.MegaPassword
	megaSessMu.Lock()
	defer megaSessMu.Unlock()
	if megaSIDFor == key && (megaSID != "" || megaLoginErr != nil) {
		return megaSID
	}
	sid, err := megaLogin(ctx, cfg.MegaEmail, cfg.MegaPassword)
	megaSIDFor, megaSID, megaLoginErr = key, sid, err
	if err != nil {
		log.Printf("[MEGA] %v, downloading without an account", err)
		return ""
	}
	log.Printf("[MEGA] logged in as %s", cfg.MegaEmail)
	return sid
}

// megaResetSession forgets the session (expired), the next call logs in again.
func megaResetSession() {
	megaSessMu.Lock()
	megaSID, megaSIDFor, megaLoginErr = "", "", nil
	megaSessMu.Unlock()
}

// solveHashcash answers MEGA's "X-Hashcash: 1:<easiness>:<time>:<token>" challenge
// (same as the official SDK: SHA-256 over a 4 byte prefix + the token 262144 times,
// until the first 4 bytes are <= threshold). Returns the header value to send back.
func solveHashcash(ctx context.Context, challenge string) (string, error) {
	parts := strings.Split(challenge, ":")
	if len(parts) != 4 || parts[0] != "1" {
		return "", fmt.Errorf("MEGA: unknown hashcash %q", challenge)
	}
	var e int
	if _, err := fmt.Sscanf(parts[1], "%d", &e); err != nil || e < 0 || e > 255 {
		return "", fmt.Errorf("MEGA: bad hashcash easiness")
	}
	token, err := megaB64(parts[3])
	if err != nil || len(token) != 48 {
		return "", fmt.Errorf("MEGA: bad hashcash token")
	}
	threshold := uint32(((e&63)<<1)+1) << uint((e>>6)*7+3)
	const reps = 262144
	buf := make([]byte, 4+reps*48)
	for i := 0; i < reps; i++ {
		copy(buf[4+i*48:], token)
	}
	for n := uint32(0); ; n++ {
		if n%64 == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		binary.BigEndian.PutUint32(buf, n)
		sum := sha256.Sum256(buf)
		if binary.BigEndian.Uint32(sum[:4]) <= threshold {
			return "1:" + parts[3] + ":" + megaB64Enc(buf[:4]), nil
		}
	}
}
