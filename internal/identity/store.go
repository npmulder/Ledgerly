package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CreateFirstUser(ctx context.Context, email, passwordHash, name string) (user User, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin first user registration: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, "LOCK TABLE identity.users IN EXCLUSIVE MODE"); err != nil {
		return User{}, fmt.Errorf("lock users for first registration: %w", err)
	}

	var userCount int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM identity.users").Scan(&userCount); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if userCount > 0 {
		return User{}, ErrRegistrationClosed
	}

	if err := tx.QueryRow(
		ctx,
		`INSERT INTO identity.users (email, password_hash, name)
		 VALUES ($1, $2, $3)
		 RETURNING id, email, name, created_at`,
		email,
		passwordHash,
		name,
	).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt); err != nil {
		return User{}, fmt.Errorf("insert first user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit first user registration: %w", err)
	}
	return user, nil
}

func (s *PostgresStore) FindUserByEmail(ctx context.Context, email string) (storedUser, error) {
	var user storedUser
	err := s.pool.QueryRow(
		ctx,
		`SELECT id, email, password_hash, name, created_at
		 FROM identity.users
		 WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.CreatedAt)
	if err == nil {
		return user, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return storedUser{}, ErrUserNotFound
	}
	return storedUser{}, fmt.Errorf("find user by email: %w", err)
}

func (s *PostgresStore) CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error {
	_, err := s.pool.Exec(
		ctx,
		`INSERT INTO identity.sessions (token_sha256, user_id, expires_at)
		 VALUES ($1, $2, $3)`,
		tokenHash,
		userID,
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *PostgresStore) FindSessionByTokenHash(ctx context.Context, tokenHash []byte) (storedSession, error) {
	var session storedSession
	err := s.pool.QueryRow(
		ctx,
		`SELECT u.id, u.email, u.name, u.created_at, s.expires_at, s.created_at
		 FROM identity.sessions s
		 JOIN identity.users u ON u.id = s.user_id
		 WHERE s.token_sha256 = $1`,
		tokenHash,
	).Scan(
		&session.User.ID,
		&session.User.Email,
		&session.User.Name,
		&session.User.CreatedAt,
		&session.ExpiresAt,
		&session.CreatedAt,
	)
	if err == nil {
		return session, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return storedSession{}, ErrUnauthenticated
	}
	return storedSession{}, fmt.Errorf("find session: %w", err)
}

func (s *PostgresStore) RefreshSession(ctx context.Context, tokenHash []byte, expiresAt time.Time) error {
	tag, err := s.pool.Exec(
		ctx,
		`UPDATE identity.sessions
		 SET expires_at = $2
		 WHERE token_sha256 = $1`,
		tokenHash,
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("refresh session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnauthenticated
	}
	return nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM identity.sessions WHERE token_sha256 = $1", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
