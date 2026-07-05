package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/clock"
)

type storedUser struct {
	User
	PasswordHash string
}

type storedSession struct {
	User      User
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Store interface {
	UsersExist(ctx context.Context) (bool, error)
	CreateFirstUser(ctx context.Context, email, passwordHash, name string) (User, error)
	FindUserByEmail(ctx context.Context, email string) (storedUser, error)
	CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error
	FindSessionByTokenHash(ctx context.Context, tokenHash []byte) (storedSession, error)
	RefreshSession(ctx context.Context, tokenHash []byte, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

// Service owns identity auth use cases.
type Service struct {
	store          Store
	clock          clock.Clock
	passwordParams PasswordParams
	tokenReader    io.Reader
}

type ServiceOption func(*Service)

func WithPasswordParams(params PasswordParams) ServiceOption {
	return func(s *Service) {
		s.passwordParams = normalizePasswordParams(params)
	}
}

func WithTokenReader(reader io.Reader) ServiceOption {
	return func(s *Service) {
		if reader != nil {
			s.tokenReader = reader
		}
	}
}

func NewService(store Store, clk clock.Clock, opts ...ServiceOption) *Service {
	if clk == nil {
		clk = clock.New()
	}

	service := &Service{
		store:          store,
		clock:          clk,
		passwordParams: DefaultPasswordParams(),
		tokenReader:    rand.Reader,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return User{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return User{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(input.Password) == "" {
		return User{}, fmt.Errorf("password is required")
	}

	closed, err := s.store.UsersExist(ctx)
	if err != nil {
		return User{}, err
	}
	if closed {
		return User{}, ErrRegistrationClosed
	}

	hash, err := HashPassword(input.Password, s.passwordParams, s.tokenReader)
	if err != nil {
		return User{}, err
	}

	return s.store.CreateFirstUser(ctx, email, hash, name)
}

func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	user, err := s.store.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, err
	}

	ok, err := VerifyPassword(input.Password, user.PasswordHash)
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := newSessionToken(s.tokenReader)
	if err != nil {
		return LoginResult{}, fmt.Errorf("create session token: %w", err)
	}
	now := s.clock.Now().UTC()
	if err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return LoginResult{}, err
	}

	expiresAt := now.Add(sessionDuration)
	tokenHash := hashSessionToken(token)
	if err := s.store.CreateSession(ctx, user.ID, tokenHash[:], expiresAt); err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		User:      user.User,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}

	tokenHash := hashSessionToken(token)
	return s.store.DeleteSession(ctx, tokenHash[:])
}

func (s *Service) CheckCredential(ctx context.Context, credential Credential) (CredentialCheckResult, error) {
	if credential.Kind != CredentialKindSessionCookie || strings.TrimSpace(credential.Token) == "" {
		return CredentialCheckResult{}, ErrUnauthenticated
	}

	tokenHash := hashSessionToken(credential.Token)
	session, err := s.store.FindSessionByTokenHash(ctx, tokenHash[:])
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return CredentialCheckResult{}, ErrUnauthenticated
		}
		return CredentialCheckResult{}, err
	}

	now := s.clock.Now().UTC()
	if !session.ExpiresAt.After(now) {
		_ = s.store.DeleteSession(ctx, tokenHash[:])
		return CredentialCheckResult{}, ErrUnauthenticated
	}

	expiresAt := now.Add(sessionDuration)
	if err := s.store.RefreshSession(ctx, tokenHash[:], expiresAt); err != nil {
		return CredentialCheckResult{}, err
	}

	principal := Principal{
		User:      session.User,
		ExpiresAt: expiresAt,
	}
	return CredentialCheckResult{
		Principal: principal,
		Token:     credential.Token,
		ExpiresAt: expiresAt,
	}, nil
}

func normalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", fmt.Errorf("email is invalid")
	}
	return email, nil
}

func newSessionToken(reader io.Reader) (string, error) {
	var raw [32]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashSessionToken(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}
