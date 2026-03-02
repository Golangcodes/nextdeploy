package envstore

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared"
)

type Store[T any] struct {
	data     map[string]T
	filename string
}

var (
	storeLogger = shared.PackageLogger("ENV", "🌱 ENV")
)

type Option[T any] func(*Store[T]) error

func WithEnvFile[T any](filename string) Option[T] {
	return func(s *Store[T]) error {
		s.filename = filename
		envData, err := readEnvFile(filename)
		if err != nil {
			return err
		}

		for k, _ := range envData {
			var value T
			s.data[k] = value
		}
		return nil
	}
}

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

func (s *Store[T]) Create(key string, value T) error {
	if _, exists := s.data[key]; exists {
		return fmt.Errorf("key %s already exists", key)
	}
	s.data[key] = value
	return nil
}

func (s *Store[T]) Read(key string) (T, error) {
	value, exists := s.data[key]
	if !exists {
		return value, fmt.Errorf("key %s not found", key)
	}
	return value, nil
}

func (s *Store[T]) Update(key string, value T) error {
	if _, exists := s.data[key]; !exists {
		return fmt.Errorf("key %s not found", key)
	}
	s.data[key] = value
	return nil
}

func (s *Store[T]) Delete(key string) error {
	if _, exists := s.data[key]; !exists {
		return fmt.Errorf("key %s not found", key)
	}
	delete(s.data, key)
	return nil
}

func (s *Store[T]) GetEnv(key string) (string, error) {
	if value, exists := os.LookupEnv(key); exists {
		return value, nil
	}

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

func CreateFn[T any](key string, value T) func(*Store[T]) error {
	return func(s *Store[T]) error {
		return s.Create(key, value)
	}
}

func ReadFn[T any](key string, fn func(T) error) func(*Store[T]) error {
	return func(s *Store[T]) error {
		value, err := s.Read(key)
		if err != nil {
			return err
		}
		return fn(value)
	}
}

func UpdateFn[T any](key string, value T) func(*Store[T]) error {
	return func(s *Store[T]) error {
		return s.Update(key, value)
	}
}

func DeleteFn[T any](key string) func(*Store[T]) error {
	return func(s *Store[T]) error {
		return s.Delete(key)
	}
}
