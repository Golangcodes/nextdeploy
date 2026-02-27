package envstore

import (
	"bufio"
	"fmt"
	"nextdeploy/shared"
	"os"
	"strings"
)

type Store[T any] struct {
	data     map[string]T
	filename string
}

var (
	storeLogger = shared.PackageLogger("ENV", "🌱 ENV")
)

// Option is a functional option pattern type for configuring Store
type Option[T any] func(*Store[T]) error

// WithEnvFile configures the store to load initial data from an env file
func WithEnvFile[T any](filename string) Option[T] {
	return func(s *Store[T]) error {
		s.filename = filename
		envData, err := readEnvFile(filename)
		if err != nil {
			return err
		}

		// Convert env data to type T - this is simplistic and would need adaptation
		// based on how you want to map env vars to your type T
		for k, _ := range envData {
			// This is a placeholder - actual conversion depends on T
			var value T
			// You'd need type-specific conversion here
			s.data[k] = value
		}
		return nil
	}
}

// New creates a new Store with optional configurations
func New[T any](opts ...Option[T]) (*Store[T], error) {
	s := &Store[T]{
		data: make(map[string]T),
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Create adds a new item to the store
func (s *Store[T]) Create(key string, value T) error {
	if _, exists := s.data[key]; exists {
		return fmt.Errorf("key %s already exists", key)
	}
	s.data[key] = value
	return nil
}

// Read retrieves an item from the store
func (s *Store[T]) Read(key string) (T, error) {
	value, exists := s.data[key]
	if !exists {
		return value, fmt.Errorf("key %s not found", key)
	}
	return value, nil
}

// Update modifies an existing item in the store
func (s *Store[T]) Update(key string, value T) error {
	if _, exists := s.data[key]; !exists {
		return fmt.Errorf("key %s not found", key)
	}
	s.data[key] = value
	return nil
}

// Delete removes an item from the store
func (s *Store[T]) Delete(key string) error {
	if _, exists := s.data[key]; !exists {
		return fmt.Errorf("key %s not found", key)
	}
	delete(s.data, key)
	return nil
}

// Env variable operations

// GetEnv reads a single value from the environment or env file
func (s *Store[T]) GetEnv(key string) (string, error) {
	// First try OS environment
	if value, exists := os.LookupEnv(key); exists {
		return value, nil
	}

	// Fall back to env file if configured
	if s.filename != "" {
		envData, err := readEnvFile(s.filename)
		if err != nil {
			return "", err
		}
		if value, exists := envData[key]; exists {
			return value, nil
		}
	}

	return "", fmt.Errorf("environment variable %s not found", key)
}

// Helper function to read env file
func readEnvFile(filename string) (map[string]string, error) {
	// #nosec G304
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	storeLogger.Debug("Reading env file: %v", filename)
	defer func() {
		if err := file.Close(); err != nil {
			storeLogger.Error("Failed to close env file: %v", err)
		}
	}()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		env[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return env, nil
}

// Functional variants that return functions for composition

// CreateFn returns a function that performs a Create operation
func CreateFn[T any](key string, value T) func(*Store[T]) error {
	return func(s *Store[T]) error {
		return s.Create(key, value)
	}
}

// ReadFn returns a function that performs a Read operation
func ReadFn[T any](key string, fn func(T) error) func(*Store[T]) error {
	return func(s *Store[T]) error {
		value, err := s.Read(key)
		if err != nil {
			return err
		}
		return fn(value)
	}
}

// UpdateFn returns a function that performs an Update operation
func UpdateFn[T any](key string, value T) func(*Store[T]) error {
	return func(s *Store[T]) error {
		return s.Update(key, value)
	}
}

// DeleteFn returns a function that performs a Delete operation
func DeleteFn[T any](key string) func(*Store[T]) error {
	return func(s *Store[T]) error {
		return s.Delete(key)
	}
}
