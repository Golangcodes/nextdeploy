package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func PromptForConfig(reader *bufio.Reader) (*NextDeployConfig, error) {
	cfg := &NextDeployConfig{
		Version: "1.0",
		App: AppConfig{
			Port: 3000,
		},
	}

	if err := PromptAppConfig(reader, cfg); err != nil {
		return nil, fmt.Errorf("app configuration error: %w", err)
	}

	if err := PromptRepositoryConfig(reader, cfg); err != nil {
		return nil, fmt.Errorf("repository configuration error: %w", err)
	}

	if err := PromptTargetType(reader, cfg); err != nil {
		return nil, fmt.Errorf("target type configuration error: %w", err)
	}

	if cfg.TargetType == "serverless" {
		if err := PromptServerlessConfig(reader, cfg); err != nil {
			return nil, fmt.Errorf("serverless configuration error: %w", err)
		}
	} else {
		if err := PromptVPSConfig(reader, cfg); err != nil {
			return nil, fmt.Errorf("vps configuration error: %w", err)
		}
	}

	if PromptYesNo(reader, "Configure database?") {
		dbConfig, err := PromptDatabaseConfig(reader)
		if err != nil {
			return nil, fmt.Errorf("database configuration error: %w", err)
		}
		cfg.Database = &dbConfig
	}

	if PromptYesNo(reader, "Configure monitoring?") {
		monConfig, err := PromptMonitoringConfig(reader)
		if err != nil {
			return nil, fmt.Errorf("monitoring configuration error: %w", err)
		}
		cfg.Monitoring = &monConfig
	}

	return cfg, nil
}

func PromptAppConfig(reader *bufio.Reader, cfg *NextDeployConfig) error {
	fmt.Print("Enter application name: ")
	name, err := ReadRequiredInput(reader)
	if err != nil {
		return err
	}
	cfg.App.Name = name

	fmt.Print("Environment (production/staging): ")
	env, err := ReadRequiredInput(reader)
	if err != nil {
		return err
	}
	cfg.App.Environment = env

	fmt.Print("Domain (leave empty if none): ")
	cfg.App.Domain, _ = reader.ReadString('\n')
	cfg.App.Domain = strings.TrimSpace(cfg.App.Domain)

	return nil
}

func PromptRepositoryConfig(reader *bufio.Reader, cfg *NextDeployConfig) error {
	fmt.Print("Git repository URL (e.g., git@github.com:user/repo.git): ")
	url, err := ReadRequiredInput(reader)
	if err != nil {
		return err
	}
	cfg.Repository.URL = url

	fmt.Print("Git branch (default: main): ")
	branch, _ := reader.ReadString('\n')
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "main"
	}
	cfg.Repository.Branch = branch

	cfg.Repository.AutoDeploy = PromptYesNo(reader, "Enable auto-deploy?")

	if cfg.Repository.AutoDeploy {
		fmt.Print("Webhook secret (leave empty to generate): ")
		secret, _ := reader.ReadString('\n')
		cfg.Repository.WebhookSecret = strings.TrimSpace(secret)
	}

	return nil
}

func PromptTargetType(reader *bufio.Reader, cfg *NextDeployConfig) error {
	fmt.Println("\n--- DEPLOYMENT TARGET ---")
	fmt.Println("Choose where you want to deploy your application:")
	fmt.Println("1) serverless (AWS Lambda + S3 + CloudFront)")
	fmt.Println("2) vps (Traditional Linux Server)")
	fmt.Print("Selection (1/2, default: 1): ")

	selection, _ := reader.ReadString('\n')
	selection = strings.TrimSpace(selection)

	if selection == "2" || strings.ToLower(selection) == "vps" {
		cfg.TargetType = "vps"
	} else {
		cfg.TargetType = "serverless"
	}
	return nil
}

func PromptServerlessConfig(reader *bufio.Reader, cfg *NextDeployConfig) error {
	fmt.Println("\n--- SERVERLESS ARCHITECTURE (AWS) ---")
	fmt.Println("NextDeploy will automatically provision your AWS resources during 'ship':")
	fmt.Println("✅ S3 Bucket (Assets/Storage)")
	fmt.Println("✅ AWS Lambda (Compute/Functions)")
	fmt.Println("✅ Secrets Manager (Encrypted Secrets)")

	cfg.Serverless = &ServerlessConfig{Provider: "aws"}

	fmt.Print("AWS Region (e.g., us-east-1): ")
	region, err := ReadRequiredInput(reader)
	if err != nil {
		return err
	}
	cfg.Serverless.Region = region

	fmt.Print("AWS Profile (default: default): ")
	profile, _ := reader.ReadString('\n')
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = "default"
	}
	cfg.Serverless.Profile = profile

	fmt.Println("\n[SECURITY] NextDeploy requires an IAM Role to create your Lambda function.")
	fmt.Println("This role only needs basic Lambda execution permissions.")
	fmt.Print("IAM Role ARN (e.g. arn:aws:iam::...:role/name): ")
	role, err := ReadRequiredInput(reader)
	if err != nil {
		return err
	}
	cfg.Serverless.IAMRole = role

	// Mirror to CloudProvider for broader SDK use
	cfg.CloudProvider = &CloudProviderStruct{
		Name:    "aws",
		Region:  region,
		Profile: profile,
	}

	return nil
}

func PromptVPSConfig(reader *bufio.Reader, cfg *NextDeployConfig) error {
	fmt.Println("\n--- VPS CONFIGURATION ---")
	var server ServerConfig

	fmt.Print("Server name (e.g. prod-01): ")
	server.Name, _ = ReadRequiredInput(reader)

	fmt.Print("Server Host/IP: ")
	server.Host, _ = ReadRequiredInput(reader)

	fmt.Print("SSH Username (e.g. ubuntu): ")
	server.Username, _ = ReadRequiredInput(reader)

	fmt.Print("SSH Key Path (e.g. ~/.ssh/id_rsa): ")
	server.KeyPath, _ = ReadRequiredInput(reader)

	server.Port = 22
	cfg.Servers = append(cfg.Servers, server)

	return nil
}

func PromptDatabaseConfig(reader *bufio.Reader) (Database, error) {
	var db Database

	fmt.Print("Database type (mysql/postgres): ")
	dbType, err := ReadRequiredInput(reader)
	if err != nil {
		return db, err
	}
	db.Type = dbType

	fmt.Print("Database host (leave empty for localhost): ")
	host, _ := reader.ReadString('\n')
	host = strings.TrimSpace(host)
	if host == "" {
		host = "localhost"
	}
	db.Host = host

	fmt.Print("Database port: ")
	port, err := ReadRequiredInput(reader)
	fmt.Println(port)
	if err != nil {
		return db, err
	}
	fmt.Print("Database username: ")
	username, err := ReadRequiredInput(reader)
	if err != nil {
		return db, err
	}
	db.Username = username

	fmt.Print("Database password: ")
	password, err := ReadRequiredInput(reader)
	if err != nil {
		return db, err
	}
	db.Password = password

	fmt.Print("Database name: ")
	name, err := ReadRequiredInput(reader)
	if err != nil {
		return db, err
	}
	db.Name = name

	return db, nil
}

func PromptMonitoringConfig(reader *bufio.Reader) (Monitoring, error) {
	var mon Monitoring

	mon.Enabled = true

	fmt.Print("Monitoring type (prometheus/grafana): ")
	monType, err := ReadRequiredInput(reader)
	if err != nil {
		return mon, err
	}
	mon.Type = monType

	fmt.Print("Monitoring endpoint (leave empty for default): ")
	endpoint, _ := reader.ReadString('\n')
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		if mon.Type == "prometheus" {
			endpoint = "http://localhost:9090"
		} else {
			endpoint = "http://localhost:3000"
		}
	}
	mon.Endpoint = endpoint

	return mon, nil
}

func PromptYesNo(reader *bufio.Reader, question string) bool {
	fmt.Printf("%s (y/n): ", question)
	resp, _ := reader.ReadString('\n')
	resp = strings.TrimSpace(strings.ToLower(resp))
	return resp == "y" || resp == "yes"
}

func ReadRequiredInput(reader *bufio.Reader) (string, error) {
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("this field is required")
	}
	return input, nil
}

func WriteConfig(filename string, cfg *NextDeployConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	// #nosec G302 G703
	if err := os.WriteFile(filename, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	fmt.Printf("Configuration saved to %s\n", filename)
	return nil
}
