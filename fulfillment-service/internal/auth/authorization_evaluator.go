package auth

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/storage/inmem"
	"k8s.io/apimachinery/pkg/util/validation"

	k8sfiles "github.com/osac-project/osac/fulfillment-service/internal/kubernetes/files"
)

// k8sTokenFile is the value of the 'namespace' external data item passed to the Rego policy when we aren't running
// inside a Kubernetes pod.
const grpcAuthzDefaultNamespace = "osac"

//go:embed policies/authz.rego
var authzPolicy string

// EvaluatorBuilder contains the data and logic needed to build an evaluator that checks authorization
// using an embedded Rego policy evaluated with the OPA library. Don't create instances of this type directly, use the
type EvaluatorBuilder struct {
	emergencyServiceAccounts []string
}

// AuthzDecision contains the result of an authorization evaluation.
type AuthzDecision struct {
	Allowed        bool
	Reason         string
	SubjectUser    string   // The user extracted from OPA policy output (subject_user)
	SubjectTenants []string // The tenants extracted from OPA policy output (subject_tenant_result)
}

// AuthContext holds the complete identity claims extracted from a JWT token.
// This structure preserves all claims needed for OPA policy evaluation, ensuring
// identical authorization decisions for both actual operations and permission checks.
type AuthContext struct {
	// AuthMethod indicates the authentication method: "jwt" or "serviceaccount"
	AuthMethod string

	// Username is the authenticated user's name, extracted from preferred_username or username claim
	Username string

	// Groups contains the user's group memberships
	Groups []any

	// Organization contains the organization claim, which can be an array or object
	Organization any

	// Organizations contains the organizations claim array
	Organizations []any

	// RealmAccess is the realm access of the user's authentication
	RealmAccess map[string]any

	// The following fields are for context extensions passed to OPA as input.context.context_extensions.
	// Project name, currently only used for project membership authorization.
	Project string
	// ID is the identifier of the incoming request
	ID string
	// Tenant of the object requested by the incoming authorization request
	Tenant string
	// Name of the object requested by the incoming authorization request, currently only used for project operations.
	Name string
}

// ContextExtensionToMap converts the context extensions to the untyped map OPA expects, omitting empty fields.
func ContextExtensionToMap(authContext *AuthContext) map[string]any {
	m := map[string]any{}
	if authContext.ID != "" {
		m["id"] = authContext.ID
	}
	if authContext.Tenant != "" {
		m["tenant"] = authContext.Tenant
	}
	if authContext.Name != "" {
		m["name"] = authContext.Name
	}
	if authContext.Project != "" {
		m["project"] = authContext.Project
	}
	return m
}

// AuthorizationEvaluator defines the interface for evaluating authorization decisions.
// Implementations must evaluate whether a given authentication context is authorized
// to perform a method with the specified context extensions.
//
// This interface enables:
// - Dependency injection for testing (mock evaluators)
// - Alternative authorization backends (future: other policy engines)
// - Consistent authorization evaluation across permission checks and actual operations
type AuthorizationEvaluator interface {
	// Evaluate determines whether the authenticated subject is authorized to perform
	// the specified method with the given context extensions.
	//
	// authContext contains the complete JWT claims (username, groups, roles, organization,
	// tenants, auth method) extracted by the authentication interceptor.
	// method is the gRPC method path (e.g., "/osac.public.v1.Clusters/Create").
	//
	// Returns an AuthzDecision indicating whether the operation is allowed and an
	// optional reason string. Returns an error if the evaluation itself fails
	// (e.g., policy engine unreachable, timeout, compilation error).
	Evaluate(
		ctx context.Context,
		authContext *AuthContext,
		method string,
	) (*AuthzDecision, error)
}

// OPAAuthorizationEvaluator implements AuthorizationEvaluator using OPA (Open Policy Agent).
// This is the production implementation that evaluates authorization using the existing
// OPA policy (authz.rego).
type OPAAuthorizationEvaluator struct {
	query rego.PreparedEvalQuery
}

// NewEvaluator creates a new Evaluator that can be used to evaluate authorization decisions.
func NewEvaluator() *EvaluatorBuilder {
	return &EvaluatorBuilder{}
}

func (b *EvaluatorBuilder) AddEmergencyServiceAccounts(value []string) *EvaluatorBuilder {
	b.emergencyServiceAccounts = value
	return b
}

// Build creates an OPAAuthorizationEvaluator with the embedded authz.rego policy.
func (b *EvaluatorBuilder) Build() (*OPAAuthorizationEvaluator, error) {
	emergencyServiceAccounts, err := buildEmergencyServiceAccounts(b.emergencyServiceAccounts)
	if err != nil {
		return nil, fmt.Errorf("failed to build emergency service accounts: %w", err)
	}

	policyData := inmem.NewFromObject(map[string]any{
		"emergency_service_accounts": emergencyServiceAccounts,
	})

	query, err := rego.New(
		rego.Query("data.authz"),
		rego.Module("authz.rego", authzPolicy),
		rego.Store(policyData),
	).PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to compile authorization policy: %w", err)
	}

	// Create the evaluator:
	result := &OPAAuthorizationEvaluator{
		query: query,
	}
	return result, nil
}

func buildEmergencyServiceAccounts(emergencyServiceAccounts []string) ([]any, error) {
	// If we are running in a Kubernetes pod we want to add a 'nsName' to the external data, so that it can be
	// used by the policy to construct the full names of Kubernetes service accounts.
	var nsName string
	nsBytes, err := os.ReadFile(k8sfiles.ServiceAccountNamespace)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			nsName = grpcAuthzDefaultNamespace
		} else {
			return nil, fmt.Errorf(
				"failed to read Kubernetes namespace file '%s': %w",
				k8sfiles.ServiceAccountNamespace, err,
			)
		}
	} else {
		nsName = strings.TrimSpace(string(nsBytes))
		if nsName == "" {
			nsName = grpcAuthzDefaultNamespace
		}
	}

	// Validate and build the full Kubernetes service account names for the emergency accounts:
	emergencyServiceAccountsAny := make([]any, 0, len(emergencyServiceAccounts))
	for _, emergencyServiceAccount := range emergencyServiceAccounts {
		emergencyServiceAccount = strings.TrimSpace(emergencyServiceAccount)
		errs := validation.IsDNS1123Subdomain(emergencyServiceAccount)
		if len(errs) > 0 {
			return nil, fmt.Errorf(
				"emergency service account name '%s' is not a valid Kubernetes service account name",
				emergencyServiceAccount,
			)
		}

		emergencyServiceAccountName := fmt.Sprintf(
			"system:serviceaccount:%s:%s",
			nsName, emergencyServiceAccount,
		)
		emergencyServiceAccountsAny = append(emergencyServiceAccountsAny, emergencyServiceAccountName)
	}
	return emergencyServiceAccountsAny, nil
}

// Evaluate evaluates OPA policy for a given authentication context, method, and context extensions.
// This method is called by both GrpcAuthzInterceptor (for actual operations) and
// SelfSubjectAccessReviews.Create (for hypothetical permission checks).
//
// authContext contains the complete JWT claims extracted by the authentication interceptor.
// This ensures OPA receives identical input for both permission checks and actual operations.
func (e *OPAAuthorizationEvaluator) Evaluate(
	ctx context.Context,
	authContext *AuthContext,
	method string,
) (*AuthzDecision, error) {
	if authContext == nil {
		return nil, errors.New("authContext is mandatory")
	}
	if method == "" {
		return nil, errors.New("method is mandatory")
	}

	input := constructOPAInput(authContext, method)

	// Query the OPA policy. Following GrpcAuthzInterceptor pattern:
	// query is "data.authz", result is results[0].Expressions[0].Value as map[string]any
	results, err := e.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, fmt.Errorf("OPA evaluation failed: %w", err)
	}
	if len(results) == 0 {
		return &AuthzDecision{Allowed: false, Reason: "no policy result"}, nil
	}

	// Extract the authz data map
	authzData, ok := results[0].Expressions[0].Value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OPA returned unexpected result type")
	}

	// Extract allow boolean
	allow, _ := authzData["allow"].(bool)

	// Extract subject data from OPA output
	subjectUser, _ := authzData["subject_user"].(string)

	var subjectTenants []string
	if tenantValues, ok := authzData["subject_tenant_result"].([]any); ok {
		for _, v := range tenantValues {
			if s, ok := v.(string); ok {
				subjectTenants = append(subjectTenants, s)
			}
		}
	}

	decision := &AuthzDecision{
		Allowed:        allow,
		SubjectUser:    subjectUser,
		SubjectTenants: subjectTenants,
	}

	// Note: Current OPA policy does not return a "reason" field.
	// If denied, the reason is implicit from the policy rules.
	// Future enhancement: add reason field to authz.rego policy output.

	return decision, nil
}

// constructOPAInput builds the input structure for OPA policy evaluation.
// This preserves the complete identity structure from GrpcAuthzInterceptor.buildInput,
// ensuring identical OPA input for both actual operations and permission checks.
// authContext contains all JWT claims needed for complete authorization evaluation.
func constructOPAInput(authContext *AuthContext, method string) map[string]any {
	// Build the identity document, matching the structure from GrpcAuthzInterceptor.buildInput
	identity := map[string]any{
		"authnMethod": authContext.AuthMethod,
	}

	if authContext.AuthMethod == "serviceaccount" {
		// For Kubernetes service accounts, nest username and groups under "user"
		// to match the OPA policy expectation: input.auth.identity.user.username
		user := map[string]any{}
		if authContext.Username != "" {
			user["username"] = authContext.Username
		}
		if len(authContext.Groups) > 0 {
			user["groups"] = authContext.Groups
		}
		identity["user"] = user
	} else {
		// For JWT tokens, include username at top level and conditionally include claims
		if authContext.Username != "" {
			identity["username"] = authContext.Username
		}
		if len(authContext.Groups) > 0 {
			identity["groups"] = authContext.Groups
		}
		if authContext.Organization != nil {
			identity["organization"] = authContext.Organization
		}
		if len(authContext.Organizations) > 0 {
			identity["organizations"] = authContext.Organizations
		}
		if len(authContext.RealmAccess) > 0 {
			identity["realm_access"] = authContext.RealmAccess
		}
	}

	return map[string]any{
		"context": map[string]any{
			"request": map[string]any{
				"http": map[string]any{
					"path": method,
				},
			},
			"context_extensions": ContextExtensionToMap(authContext),
		},
		"auth": map[string]any{
			"identity": identity,
		},
	}
}
