package config

import (
	"bufio"
	"fmt"
	"github.com/Golangcodes/nextdeploy/shared"
	"strconv"
	"strings"
)

var (
	dlog   = shared.PackageLogger("docker", "🧾 DOCKER")
	applog = shared.PackageLogger("app", "➤ APP")
	clog   = shared.PackageLogger("config", "🔧 CONFIG")
)

func InteractiveConfigPrompt(reader *bufio.Reader) (*NextDeployConfig, error) {
	cfg := &NextDeployConfig{
		Version: "1.0",
	}

	dlog.Info("\n✨ Welcome to NextDeploy Configuration Wizard ✨")
	dlog.Info("This interactive guide will help you set up all the components needed to deploy your application.")
	dlog.Info("We'll walk through each section step by step with explanations along the way.")
	dlog.Info("You can press Enter to accept default values where available.\n")

	// App Configuration
	dlog.Info("\n📱 Application Settings")
	fmt.Println("----------------------")
	dlog.Info("These settings define your application's basic properties and runtime environment.")
	cfg.App = promptAppConfig(reader)

	// Repository Configuration
	dlog.Info("\n📦 Repository Settings")
	fmt.Println("----------------------")
	dlog.Info("Configure your source code repository for automatic deployments.")
	cfg.Repository = promptRepositoryConfig(reader)

	// Docker Configuration
	dlog.Info("\n🐳 Docker Settings")
	fmt.Println("----------------------")
	dlog.Info("Define how your application should be containerized and built.")
	cfg.Docker = promptDockerConfig(reader)

	// Deployment Configuration
	dlog.Info("\n🚀 Deployment Settings")
	fmt.Println("----------------------")
	dlog.Info("Specify where and how your application should be deployed.")
	cfg.Deployment = promptDeploymentConfig(reader)

	// Database Configuration
	if promptYesNo(reader, "Does your application use a database? (We'll configure PostgreSQL/MySQL/MongoDB connection)", false) {
		dlog.Info("\n💾 Database Settings")
		fmt.Println("----------------------")
		dlog.Info("Configure connection details for your database server.")
		dbConfig := promptDatabaseConfig(reader)
		cfg.Database = &dbConfig
	}

	// Monitoring Configuration
	if promptYesNo(reader, "Would you like to set up monitoring for your application? (CPU, memory, health checks)", false) {
		dlog.Info("\n👀 Monitoring Settings")
		fmt.Println("----------------------")
		dlog.Info("Set up monitoring and alerting for your deployed application.")
		monitoringConfig := promptMonitoringConfig(reader)
		cfg.Monitoring = &monitoringConfig
	}

	fmt.Println("\n✅ Configuration Complete!")
	ShowConfigSummary(cfg)

	if !promptYesNo(reader, "Does this configuration look correct? Would you like to save it?", true) {
		dlog.Info("Restarting configuration...")
		return InteractiveConfigPrompt(reader)
	}

	return cfg, nil
}

func promptAppConfig(reader *bufio.Reader) AppConfig {
	fmt.Printf("\n%s What's your application name? (e.g., 'my-web-app')\n", EmojiInput)
	dlog.Info("This will be used for container naming, logging, and service identification.")
	name := readRequiredInput(reader, "application name")

	fmt.Printf("\n%s Which port does your app run on internally? (default: 3000)\n", EmojiInput)
	dlog.Info("This is the port your application listens on inside the container.")
	portStr := readInputWithDefault(reader, "3000")
	port, _ := strconv.Atoi(portStr)

	fmt.Printf("\n%s What environment is this configuration for? (dev/stage/prod, default: prod)\n", EmojiInput)
	fmt.Println("This affects environment variables, logging levels, and deployment behavior.")
	env := readInputWithDefault(reader, "prod")

	fmt.Printf("\n%s Your production domain (e.g., 'nextdeploy.one', leave empty if none yet)\n", EmojiInput)
	fmt.Println("This will be used for SSL certificate generation and deployment routing.")
	domain, _ := reader.ReadString('\n')
	domain = strings.TrimSpace(domain)

	// email for ssl -setup
	fmt.Printf("\n%s Enter your email for SSL certificate setup (e.g., 'next@next.com')\n", EmojiInput)
	fmt.Println("This email is used for Let's Encrypt SSL certificate registration.")

	// Secrets configuration
	secrets := &SecretsConfig{
		Provider: "doppler",
		Project:  "The name of your project",
		Config:   "prod",
		token:    "your-doppler-token",
	}

	return AppConfig{
		Name:        strings.TrimSpace(name),
		Port:        port,
		Environment: strings.ToLower(strings.TrimSpace(env)),
		Domain:      strings.TrimSpace(domain),
		Secrets:     secrets,
	}
}

func promptRepositoryConfig(reader *bufio.Reader) Repository {
	fmt.Printf("\n%s Git repository URL (SSH or HTTPS):\n", EmojiInput)
	fmt.Println("Example SSH: git@github.com:yourname/your-repo.git")
	fmt.Println("Example HTTPS: https://github.com/yourname/your-repo.git")
	url := readRequiredInput(reader, "repository URL")

	fmt.Printf("\n%s Which branch should we deploy from? (default: main)\n", EmojiInput)
	branch := readInputWithDefault(reader, "main")

	autoDeploy := promptYesNo(reader, "\nWould you like to enable automatic deployments when code is pushed to this branch?", true)

	var webhookSecret string
	if autoDeploy {
		fmt.Printf("\n%s Webhook secret (leave empty to generate a secure one automatically)\n", EmojiImportant)
		fmt.Println("This secret verifies that deployment requests come from your repository.")
		webhookSecret, _ = reader.ReadString('\n')
	}

	return Repository{
		URL:           strings.TrimSpace(url),
		Branch:        strings.TrimSpace(branch),
		AutoDeploy:    autoDeploy,
		WebhookSecret: strings.TrimSpace(webhookSecret),
	}
}

func promptDockerConfig(reader *bufio.Reader) DockerConfig {
	fmt.Printf("\n%s Docker image name (e.g., 'username/my-app' or 'ghcr.io/username/my-app'):\n", EmojiContainer)
	fmt.Println("This will be used to tag and push your container image.")
	image := readRequiredInput(reader, "Docker image name")

	fmt.Printf("\n%s Docker registry (leave empty for Docker Hub, or specify like 'ghcr.io' for GitHub Container Registry)\n", EmojiContainer)
	registry, _ := reader.ReadString('\n')

	// Docker build settings
	fmt.Println("\n🔨 Docker Build Settings")
	fmt.Printf("%s Build context path (default: current directory '.')\n", EmojiInput)
	fmt.Println("This is the directory containing your application code and Dockerfile.")
	context := readInputWithDefault(reader, ".")

	fmt.Printf("\n%s Dockerfile path (default: 'Dockerfile' in the build context)\n", EmojiInput)
	dockerfile := readInputWithDefault(reader, "Dockerfile")

	noCache := promptYesNo(reader, "\nShould we disable Docker build cache? (Slows builds but ensures fresh dependencies)", false)
	push := promptYesNo(reader, "Should we push the image to the registry after successful build?", true)

	// Build args
	args := make(map[string]string)
	if promptYesNo(reader, "\nWould you like to add any build arguments? (e.g., NODE_ENV=production)", false) {
		fmt.Println("\nEnter build arguments in KEY=VALUE format (one per line, empty to finish):")
		fmt.Println("Example: NODE_ENV=production")
		for {
			fmt.Printf("%s Build argument: ", EmojiInput)
			arg, _ := reader.ReadString('\n')
			arg = strings.TrimSpace(arg)
			if arg == "" {
				break
			}
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				args[parts[0]] = parts[1]
				fmt.Printf("✓ Added build arg: %s=%s\n", parts[0], parts[1])
			} else {
				fmt.Printf("%s Invalid format. Please use KEY=VALUE\n", EmojiWarning)
			}
		}
	}

	return DockerConfig{
		Image:    strings.TrimSpace(image),
		Registry: strings.TrimSpace(registry),
		Build: DockerBuild{
			Context:    context,
			Dockerfile: dockerfile,
			NoCache:    noCache,
			Args:       args,
		},
		Push: push,
	}
}

func promptDeploymentConfig(reader *bufio.Reader) Deployment {
	fmt.Println("\n🖥️  Server Connection Details")
	fmt.Printf("%s Server hostname or IP address:\n", EmojiNetwork)
	fmt.Println("This is where your application will be deployed.")
	host := readRequiredInput(reader, "server host")

	fmt.Printf("\n%s SSH username for deployment:\n", EmojiNetwork)
	fmt.Println("This user should have permissions to deploy containers.")
	user := readRequiredInput(reader, "SSH username")

	fmt.Printf("\n%s Path to SSH private key:\n", EmojiNetwork)
	fmt.Println("Example: ~/.ssh/id_rsa or /home/user/.ssh/deploy_key")
	sshKey := readRequiredInput(reader, "SSH key path")

	useSudo := promptYesNo(reader, "\nDoes the deployment user require sudo for Docker commands?", false)

	fmt.Println("\n📦 Container Settings")
	fmt.Printf("%s Container name (default: app name):\n", EmojiContainer)
	name := readInputWithDefault(reader, "")

	fmt.Printf("\n%s Container restart policy (always/unless-stopped/no, default: always):\n", EmojiContainer)
	fmt.Println("'always' ensures your app restarts automatically if it crashes or the server reboots.")
	restart := readInputWithDefault(reader, "always")

	// Port mappings
	var ports []string
	fmt.Println("\n🌐 Port Mappings")
	fmt.Println("Map server ports to container ports (e.g., '80:3000' to expose container port 3000 on server port 80)")
	fmt.Println("Enter one mapping per line, leave empty when done:")
	for {
		fmt.Printf("%s Port mapping (host:container): ", EmojiNetwork)
		port, _ := reader.ReadString('\n')
		port = strings.TrimSpace(port)
		if port == "" {
			break
		}
		if strings.Count(port, ":") != 1 {
			fmt.Printf("%s Invalid format. Use HOST_PORT:CONTAINER_PORT (e.g., 80:3000)\n", EmojiWarning)
			continue
		}
		ports = append(ports, port)
		fmt.Printf("✓ Added port mapping: %s\n", port)
	}

	return Deployment{
		Server: Server{
			Host:    strings.TrimSpace(host),
			User:    strings.TrimSpace(user),
			SSHKey:  strings.TrimSpace(sshKey),
			UseSudo: useSudo,
		},
		Container: Container{
			Name:    strings.TrimSpace(name),
			Restart: strings.TrimSpace(restart),
			Ports:   ports,
		},
	}
}

func promptDatabaseConfig(reader *bufio.Reader) Database {
	fmt.Printf("\n%s Database type (postgres/mysql/mongodb):\n", EmojiDatabase)
	dbType := readRequiredInput(reader, "database type")

	fmt.Printf("\n%s Database server hostname or IP:\n", EmojiDatabase)
	host := readRequiredInput(reader, "database host")

	fmt.Printf("\n%s Database port (default based on type):\n", EmojiDatabase)
	defaultPort := "5432"
	if strings.ToLower(dbType) == "mysql" {
		defaultPort = "3306"
	} else if strings.ToLower(dbType) == "mongodb" {
		defaultPort = "27017"
	}
	port := readInputWithDefault(reader, defaultPort)

	fmt.Printf("\n%s Database username:\n", EmojiDatabase)
	username := readRequiredInput(reader, "database username")

	fmt.Printf("\n%s Database password:\n", EmojiDatabase)
	password := readRequiredInput(reader, "database password")

	fmt.Printf("\n%s Database name:\n", EmojiDatabase)
	name := readRequiredInput(reader, "database name")

	return Database{
		Type:     strings.ToLower(strings.TrimSpace(dbType)),
		Host:     strings.TrimSpace(host),
		Port:     strings.TrimSpace(port),
		Username: strings.TrimSpace(username),
		Password: strings.TrimSpace(password),
		Name:     strings.TrimSpace(name),
	}
}

func promptMonitoringConfig(reader *bufio.Reader) Monitoring {
	fmt.Printf("\n%s Monitoring system type (e.g., 'prometheus', 'datadog', 'newrelic'):\n", EmojiInput)
	fmt.Println("This should match the monitoring system you want to integrate with.")
	monType := readRequiredInput(reader, "monitoring type")

	fmt.Printf("\n%s Monitoring endpoint URL (if applicable):\n", EmojiInput)
	fmt.Println("For some systems, this might be an API endpoint or agent URL.")
	endpoint, _ := reader.ReadString('\n')

	return Monitoring{
		Enabled:  true,
		Type:     strings.TrimSpace(monType),
		Endpoint: strings.TrimSpace(endpoint),
	}
}

// Helper function for required input
func readRequiredInput(reader *bufio.Reader, fieldName string) string {
	for {
		fmt.Printf("> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			return input
		}
		fmt.Printf("%s %s is required. Please enter a value.\n", EmojiWarning, fieldName)
	}
}

// Helper function for input with default
func readInputWithDefault(reader *bufio.Reader, defaultValue string) string {
	fmt.Printf("(default: %s) > ", defaultValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue
	}
	return input
}

func promptYesNo(reader *bufio.Reader, question string, defaultYes bool) bool {
	options := "(y/N)"
	if defaultYes {
		options = "(Y/n)"
	}

	for {
		fmt.Printf("\n%s %s %s: ", EmojiQuestion, question, options)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))

		if answer == "" {
			return defaultYes
		}
		if answer == "y" || answer == "yes" {
			return true
		}
		if answer == "n" || answer == "no" {
			return false
		}
		fmt.Printf("%s Please answer with 'y' or 'n'\n", EmojiWarning)
	}
}
