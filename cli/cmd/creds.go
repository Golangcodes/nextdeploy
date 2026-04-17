package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/credstore"
)

// providerSchemas declares the credential fields each supported provider has.
// Keep ordering deterministic — the prompt order matters for UX.
var providerSchemas = map[string][]credField{
	"cloudflare": {
		{Key: "api_token", Label: "Cloudflare API token", Required: true, Hidden: true},
		{Key: "account_id", Label: "Cloudflare account ID", Required: true, Hidden: false},
		{Key: "r2_access_key_id", Label: "R2 access key ID (optional, for R2 uploads)", Required: false, Hidden: false},
		{Key: "r2_secret_key", Label: "R2 secret access key (optional, for R2 uploads)", Required: false, Hidden: true},
	},
	"aws": {
		{Key: "access_key_id", Label: "AWS access key ID", Required: true, Hidden: false},
		{Key: "secret_access_key", Label: "AWS secret access key", Required: true, Hidden: true},
		{Key: "session_token", Label: "AWS session token (optional)", Required: false, Hidden: true},
	},
}

type credField struct {
	Key      string
	Label    string
	Required bool
	Hidden   bool
}

var credsCmd = &cobra.Command{
	Use:   "creds",
	Short: "Manage cloud-provider credentials in the encrypted credstore",
	Long: `Stores cloud-provider credentials in an AES-GCM encrypted file under
~/.nextdeploy/credstore/. Per-machine, mode 0600. Use this instead of
committing keys to nextdeploy.yml or relying on shell-exported env vars.

Resolution order at runtime: env vars → credstore → nextdeploy.yml (legacy, warns).`,
}

var credsProviderFlag string

var credsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Store credentials for a provider (interactive)",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("creds", "🔒 CREDS")
		provider := strings.ToLower(strings.TrimSpace(credsProviderFlag))
		if provider == "" {
			log.Error("--provider is required (cloudflare, aws)")
			os.Exit(2)
		}
		schema, ok := providerSchemas[provider]
		if !ok {
			log.Error("unknown provider %q (supported: cloudflare, aws)", provider)
			os.Exit(2)
		}

		existing, _ := credstore.Load(provider)
		fields := map[string]string{}
		for k, v := range existing {
			fields[k] = v
		}

		for _, f := range schema {
			val, err := promptCredential(f, fields[f.Key] != "")
			if err != nil {
				log.Error("input error: %v", err)
				os.Exit(1)
			}
			if val == "" && fields[f.Key] != "" {
				continue // keep existing
			}
			if val == "" && f.Required {
				log.Error("%s is required", f.Label)
				os.Exit(2)
			}
			if val != "" {
				fields[f.Key] = val
			}
		}

		if err := credstore.Save(provider, fields); err != nil {
			log.Error("save failed: %v", err)
			os.Exit(1)
		}
		log.Success("Stored %d field(s) for provider %q", len(fields), provider)
	},
}

var credsClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove stored credentials for a provider",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("creds", "🔒 CREDS")
		provider := strings.ToLower(strings.TrimSpace(credsProviderFlag))
		if provider == "" {
			log.Error("--provider is required")
			os.Exit(2)
		}
		if err := credstore.Delete(provider); err != nil {
			log.Error("delete failed: %v", err)
			os.Exit(1)
		}
		log.Success("Cleared credentials for provider %q", provider)
	},
}

var credsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List providers with stored credentials (names only, never values)",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("creds", "🔒 CREDS")
		providers, err := credstore.List()
		if err != nil {
			log.Error("list failed: %v", err)
			os.Exit(1)
		}
		if len(providers) == 0 {
			fmt.Println("(no credentials stored)")
			return
		}
		sort.Strings(providers)
		fmt.Println("Stored credentials:")
		for _, p := range providers {
			creds, err := credstore.Load(p)
			if err != nil {
				fmt.Printf("  %s — error: %v\n", p, err)
				continue
			}
			keys := make([]string, 0, len(creds))
			for k := range creds {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("  %s: %s\n", p, strings.Join(keys, ", "))
		}
	},
}

func promptCredential(f credField, hasExisting bool) (string, error) {
	label := f.Label
	if hasExisting {
		label += " (leave blank to keep existing)"
	}
	var ans string
	var prompt survey.Prompt
	if f.Hidden {
		prompt = &survey.Password{Message: label}
	} else {
		prompt = &survey.Input{Message: label}
	}
	if err := survey.AskOne(prompt, &ans); err != nil {
		if errors.Is(err, os.ErrClosed) || err.Error() == "interrupt" {
			return "", err
		}
		return "", err
	}
	return strings.TrimSpace(ans), nil
}

func init() {
	credsSetCmd.Flags().StringVar(&credsProviderFlag, "provider", "", "provider name (cloudflare, aws)")
	credsClearCmd.Flags().StringVar(&credsProviderFlag, "provider", "", "provider name (cloudflare, aws)")

	credsCmd.AddCommand(credsSetCmd)
	credsCmd.AddCommand(credsClearCmd)
	credsCmd.AddCommand(credsListCmd)
	rootCmd.AddCommand(credsCmd)
}
