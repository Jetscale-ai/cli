package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/spf13/cobra"
)

// Provider credential input and link payloads shared by accounts link and
// cost-sources create. Secrets come only from --credential-file.

const maxCredentialBytes = 64 << 10

var awsRoleARN = regexp.MustCompile(`^arn:aws:iam::(\d{12}):role/[\w+=,.@/-]{1,512}$`)

// readCredentialFile reads write-only secret fields as one JSON object. Only
// the provider's known secret keys are accepted; values are never echoed.
func readCredentialFile(path string, allowed []string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, usageError(errors.New("--credential-file is not readable"))
	}
	if info.Mode().IsRegular() && info.Mode().Perm()&0o077 != 0 {
		return nil, usageError(errors.New("--credential-file must not be readable by group or others"))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, usageError(errors.New("--credential-file is not readable"))
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || len(raw) > maxCredentialBytes {
		return nil, usageError(errors.New("--credential-file must be a JSON object under 64 KiB"))
	}
	var fields map[string]string
	if json.Unmarshal(raw, &fields) != nil {
		return nil, usageError(errors.New("--credential-file must be a JSON object of string fields"))
	}
	clear(raw)
	for key, value := range fields {
		if !slices.Contains(allowed, key) || value == "" {
			return nil, usageError(fmt.Errorf("--credential-file accepts only non-empty %s", strings.Join(allowed, ", ")))
		}
	}
	return fields, nil
}

var credentialKeys = map[string][]string{
	"AWS":   {"external_id"},
	"GCP":   {"service_account_key_json"},
	"AZURE": {"client_secret", "certificate", "certificate_password"},
}

// linkSpec builds the Backend integration payload from flags and secrets,
// mirroring the shapes the Backend's providers parse.
func linkSpec(cmd *cobra.Command) (api.LinkSpec, error) {
	provider := strings.ToUpper(flagString(cmd, "provider"))
	allowed, ok := credentialKeys[provider]
	if !ok {
		return api.LinkSpec{}, usageError(errors.New("--provider must be aws, azure or gcp"))
	}
	secrets, err := readCredentialFile(flagString(cmd, "credential-file"), allowed)
	if err != nil {
		return api.LinkSpec{}, err
	}
	spec := api.LinkSpec{Name: flagString(cmd, "name"), Provider: provider, Description: flagString(cmd, "description"), ProviderAccountID: flagString(cmd, "provider-account-id")}
	data := map[string]any{}
	switch provider {
	case "AWS":
		roleARN := flagString(cmd, "role-arn")
		match := awsRoleARN.FindStringSubmatch(roleARN)
		if match == nil {
			return spec, usageError(errors.New("--role-arn must be an IAM role ARN"))
		}
		if spec.ProviderAccountID == "" {
			spec.ProviderAccountID = match[1]
		} else if spec.ProviderAccountID != match[1] {
			return spec, usageError(errors.New("--provider-account-id does not match the role ARN account"))
		}
		data["CLIENT_AWS_ROLE_ARN"] = roleARN
		data["CLIENT_AWS_ROLE_EXTERNAL_ID"] = secrets["external_id"]
		if regions := flagStrings(cmd, "region"); len(regions) > 0 {
			data["CLIENT_AWS_REGIONS"] = regions
		}
	case "GCP":
		project := flagString(cmd, "project-id")
		if project == "" {
			return spec, usageError(errors.New("--project-id is required for gcp"))
		}
		if spec.ProviderAccountID != "" && spec.ProviderAccountID != project {
			return spec, usageError(errors.New("--provider-account-id must equal --project-id for gcp"))
		}
		spec.ProviderAccountID = project
		wif, email := flagString(cmd, "workload-identity-provider"), flagString(cmd, "service-account-email")
		key := secrets["service_account_key_json"]
		switch {
		case key != "" && (wif != "" || email != ""):
			return spec, usageError(errors.New("use either a service account key or workload identity federation, not both"))
		case key != "":
			data["service_account_key_json"] = key
		case wif != "" && email != "":
			data["workload_identity_provider"] = wif
			data["service_account_email"] = email
			data["subject_token_source"] = flagString(cmd, "subject-token-source")
			if quota := flagString(cmd, "quota-project-id"); quota != "" {
				data["quota_project_id"] = quota
			}
		default:
			return spec, usageError(errors.New("gcp needs --workload-identity-provider and --service-account-email, or a service_account_key_json credential"))
		}
	case "AZURE":
		tenant, client, subscription := flagString(cmd, "tenant-id"), flagString(cmd, "client-id"), strings.ToLower(flagString(cmd, "subscription-id"))
		if tenant == "" || client == "" || subscription == "" {
			return spec, usageError(errors.New("azure needs --tenant-id, --client-id and --subscription-id"))
		}
		if spec.ProviderAccountID != "" && !strings.EqualFold(spec.ProviderAccountID, subscription) {
			return spec, usageError(errors.New("--provider-account-id must equal --subscription-id for azure"))
		}
		spec.ProviderAccountID = subscription
		if (secrets["client_secret"] == "") == (secrets["certificate"] == "") {
			return spec, usageError(errors.New("azure needs exactly one of client_secret or certificate in --credential-file"))
		}
		data["tenant_id"], data["client_id"], data["subscription_id"] = tenant, client, subscription
		for _, key := range allowed {
			if secrets[key] != "" {
				data[key] = secrets[key]
			}
		}
	}
	spec.IntegrationData = data
	return spec, nil
}

func clearSecrets(data map[string]any) {
	for key := range data {
		delete(data, key)
	}
}

func (r *resourceRun) resolveAccounts(companyID string, selectors []string) ([]api.Record, error) {
	accounts := []api.Record{}
	for _, selector := range selectors {
		account, err := r.client.ResolveCloudAccount(r.ctx, companyID, selector)
		if err != nil {
			return nil, apiExit(err)
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}
