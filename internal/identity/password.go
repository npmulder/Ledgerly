package identity

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

type PasswordParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	SaltLen   uint32
	KeyLen    uint32
}

func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		MemoryKiB: 64 * 1024,
		Time:      3,
		Threads:   1,
		SaltLen:   16,
		KeyLen:    32,
	}
}

func normalizePasswordParams(params PasswordParams) PasswordParams {
	defaults := DefaultPasswordParams()
	if params.MemoryKiB == 0 {
		params.MemoryKiB = defaults.MemoryKiB
	}
	if params.Time == 0 {
		params.Time = defaults.Time
	}
	if params.Threads == 0 {
		params.Threads = defaults.Threads
	}
	if params.SaltLen == 0 {
		params.SaltLen = defaults.SaltLen
	}
	if params.KeyLen == 0 {
		params.KeyLen = defaults.KeyLen
	}
	return params
}

func HashPassword(password string, params PasswordParams, reader io.Reader) (string, error) {
	params = normalizePasswordParams(params)
	salt := make([]byte, params.SaltLen)
	if _, err := io.ReadFull(reader, salt); err != nil {
		return "", fmt.Errorf("create password salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, params.KeyLen)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		params.MemoryKiB,
		params.Time,
		params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, hash, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}

	candidate := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, params.KeyLen)
	if len(candidate) != len(hash) {
		return false, nil
	}
	return subtle.ConstantTimeCompare(candidate, hash) == 1, nil
}

func parsePasswordHash(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordParams{}, nil, nil, fmt.Errorf("password hash is not argon2id")
	}

	params, err := parsePasswordParamList(parts[3])
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("decode password salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("decode password hash: %w", err)
	}
	params.SaltLen = uint32(len(salt))
	params.KeyLen = uint32(len(hash))
	return params, salt, hash, nil
}

func parsePasswordParamList(encoded string) (PasswordParams, error) {
	var params PasswordParams
	for _, part := range strings.Split(encoded, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return PasswordParams{}, fmt.Errorf("invalid password parameter %q", part)
		}

		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return PasswordParams{}, fmt.Errorf("invalid password parameter %q: %w", key, err)
		}

		switch key {
		case "m":
			params.MemoryKiB = uint32(parsed)
		case "t":
			params.Time = uint32(parsed)
		case "p":
			if parsed > 255 {
				return PasswordParams{}, fmt.Errorf("password parallelism is too large")
			}
			params.Threads = uint8(parsed)
		default:
			return PasswordParams{}, fmt.Errorf("unknown password parameter %q", key)
		}
	}

	if params.MemoryKiB == 0 || params.Time == 0 || params.Threads == 0 {
		return PasswordParams{}, fmt.Errorf("password hash is missing argon2id parameters")
	}
	return params, nil
}
