/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	posixpath "path"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/validation"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const vastNFSSecretOwner = "system"

type vastNFSSecretsClient interface {
	SecretsClient
	List(context.Context, *privatev1.SecretsListRequest, ...grpc.CallOption) (*privatev1.SecretsListResponse, error)
}

// VASTNFSConfig holds the validated configuration and credentials for VAST NFS operations.
type VASTNFSConfig struct {
	ManagementEndpoint string
	StorageTenant      string
	Username           string
	Password           string
	RootExport         string
	ViewPolicy         string
	NFSAddress         string
}

// ResolveVASTNFSConfig reads preparation for an authorized tenant and an already selected OSAC-managed VAST backend.
func ResolveVASTNFSConfig(
	ctx context.Context,
	secrets vastNFSSecretsClient,
	tenantName string,
	backend *privatev1.StorageBackend,
) (VASTNFSConfig, error) {
	backendName := backend.GetMetadata().GetName()
	spec := backend.GetSpec()
	if len(validation.IsDNS1123Label(tenantName)) != 0 || len(validation.IsDNS1123Label(backendName)) != 0 {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS requires valid tenant and backend names")
	}
	if spec.GetProvider() != "vast" || spec.GetCspManaged() {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires an OSAC-managed VAST backend")
	}
	managementEndpoint, err := normalizeVASTManagementEndpoint(spec.GetEndpoint())
	if err != nil {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS backend requires a valid management hostname or IP, optionally prefixed with https://")
	}
	if secrets == nil {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires a SecretsService client")
	}

	if strings.Contains(tenantName, "--") || strings.Contains(backendName, "--") {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS tenant and backend names cannot contain the reserved separator '--'")
	}
	secretName := tenantName + "--" + backendName
	if len(secretName) > validation.DNS1123LabelMaxLength {
		return VASTNFSConfig{}, status.Errorf(codes.FailedPrecondition, "VAST NFS Secret name <tenant>--<backend> exceeds %d characters", validation.DNS1123LabelMaxLength)
	}
	filter := fmt.Sprintf(
		"this.metadata.tenant == %q && this.metadata.project == %q && this.metadata.name == %q",
		vastNFSSecretOwner, "", secretName,
	)
	list, err := secrets.List(ctx, privatev1.SecretsListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return VASTNFSConfig{}, vastNFSSecretReadError("list", err)
	}
	items := list.GetItems()
	if list.GetTotal() == 0 && len(items) == 0 {
		return VASTNFSConfig{}, status.Errorf(codes.NotFound, "VAST NFS preparation Secret %q is missing from the default system project", secretName)
	}
	if list.GetTotal() != 1 || len(items) != 1 {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires exactly one Secret at the expected address")
	}
	id := items[0].GetId()
	if id == "" || !matchesVASTNFSSecret(items[0], secretName) {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "listed VAST NFS preparation Secret has an invalid ID, address or deletion state")
	}

	response, err := secrets.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
	if err != nil {
		return VASTNFSConfig{}, vastNFSSecretReadError("get", err)
	}
	secret := response.GetObject()
	if secret.GetId() != id || !matchesVASTNFSSecret(secret, secretName) {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "retrieved VAST NFS preparation Secret has an unexpected ID, address or deletion state")
	}
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_OPAQUE {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation Secret must be OPAQUE")
	}
	return decodeVASTNFSConfig(secret.GetData()["config.yaml"], managementEndpoint)
}

// normalizeVASTManagementEndpoint removes the optional HTTPS scheme because
// the VAST CSI controller adds https:// when it builds its management URL.
func normalizeVASTManagementEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("management endpoint is empty")
	}

	urlValue := endpoint
	if !strings.Contains(endpoint, "://") {
		if strings.ContainsAny(endpoint, "/?#@ \t\r\n") {
			return "", fmt.Errorf("management endpoint must be a hostname or IP address")
		}
		urlValue = "https://" + endpoint
	}
	parsed, err := url.Parse(urlValue)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("management endpoint must be an HTTPS origin without credentials, path, query or fragment")
	}

	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("management endpoint host is empty")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return "", fmt.Errorf("management endpoint port cannot be empty")
	}
	if net.ParseIP(host) == nil && len(validation.IsDNS1123Subdomain(strings.ToLower(strings.TrimSuffix(host, ".")))) != 0 {
		return "", fmt.Errorf("management endpoint host must be a valid DNS name or IP address")
	}
	if port := parsed.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("management endpoint port must be between 1 and 65535")
		}
	}
	return parsed.Host, nil
}

func matchesVASTNFSSecret(secret *privatev1.Secret, name string) bool {
	metadata := secret.GetMetadata()
	return metadata.GetTenant() == vastNFSSecretOwner &&
		metadata.GetProject() == "" &&
		metadata.GetName() == name &&
		!metadata.HasDeletionTimestamp()
}

func vastNFSSecretReadError(operation string, err error) error {
	code := status.Code(err)
	if code == codes.Unknown {
		code = status.FromContextError(err).Code()
	}
	// Upstream error text can contain payload fragments.
	return status.Errorf(code, "cannot %s VAST NFS preparation Secret", operation)
}

func decodeVASTNFSConfig(payload []byte, endpoint string) (VASTNFSConfig, error) {
	var bundle struct {
		SchemaVersion any    `yaml:"schema_version"`
		Provider      string `yaml:"provider"`
		StorageTenant string `yaml:"storage_tenant"`
		Credentials   struct {
			Username string `yaml:"username"`
			Password string `yaml:"password"`
		} `yaml:"credentials"`
		Endpoints struct {
			Data string `yaml:"data"`
		} `yaml:"endpoints"`
		Options struct {
			RootExport string `yaml:"root_export"`
			ViewPolicy string `yaml:"view_policy"`
		} `yaml:"options"`
	}

	if len(payload) == 0 {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation Secret must contain config.yaml")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	decoder.KnownFields(true)
	if err := decoder.Decode(&bundle); err != nil {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation config.yaml contains invalid YAML or fields")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation config.yaml must contain one YAML document")
	}

	// Decoding directly into an int would truncate fractional YAML versions.
	version, _ := bundle.SchemaVersion.(int)
	if version != 1 || bundle.Provider != "vast" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires schema_version 1 and provider vast")
	}
	if strings.TrimSpace(bundle.StorageTenant) == "" || strings.TrimSpace(bundle.Credentials.Username) == "" || bundle.Credentials.Password == "" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires storage_tenant, credentials.username and credentials.password")
	}
	if strings.TrimSpace(bundle.Options.RootExport) == "" || strings.TrimSpace(bundle.Options.ViewPolicy) == "" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires options.root_export and options.view_policy")
	}
	rootExport, err := normalizeVASTNFSRootExport(bundle.Options.RootExport)
	if err != nil {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS options.root_export must be an absolute path without parent-directory components")
	}
	nfsAddress := strings.TrimSpace(bundle.Endpoints.Data)
	if nfsAddress == "" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires endpoints.data as a direct mount hostname or IP address")
	}
	if !validVASTNFSDataAddress(nfsAddress) {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS endpoints.data must be a fully qualified DNS name or IP address without a URL scheme")
	}
	return VASTNFSConfig{
		ManagementEndpoint: endpoint,
		StorageTenant:      bundle.StorageTenant,
		Username:           bundle.Credentials.Username,
		Password:           bundle.Credentials.Password,
		RootExport:         rootExport,
		ViewPolicy:         bundle.Options.ViewPolicy,
		NFSAddress:         nfsAddress,
	}, nil
}

func normalizeVASTNFSRootExport(rootExport string) (string, error) {
	rootExport = strings.TrimSpace(rootExport)
	if !strings.HasPrefix(rootExport, "/") {
		return "", fmt.Errorf("root export must be absolute")
	}
	for _, component := range strings.Split(rootExport, "/") {
		if component == ".." {
			return "", fmt.Errorf("root export cannot contain parent-directory components")
		}
	}
	return posixpath.Clean(rootExport), nil
}

func validVASTNFSDataAddress(address string) bool {
	ipAddress := address
	if strings.HasPrefix(ipAddress, "[") && strings.HasSuffix(ipAddress, "]") {
		ipAddress = ipAddress[1 : len(ipAddress)-1]
	}
	if net.ParseIP(ipAddress) != nil {
		return true
	}
	return len(validation.IsFullyQualifiedDomainName(nil, strings.ToLower(address))) == 0
}
