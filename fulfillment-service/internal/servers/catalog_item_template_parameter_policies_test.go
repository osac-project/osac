package servers

import (
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestCatalogItemTemplateParameterPolicies(t *testing.T) {
	value, err := anypb.New(wrapperspb.Bool(false))
	if err != nil {
		t.Fatal(err)
	}
	template := utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: privatev1.ComputeInstanceTemplate_builder{
		Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: "enabled", Required: true, Type: value.GetTypeUrl()}.Build()},
	}.Build()}
	locked := map[string]*privatev1.TemplateParameterPolicy{"enabled": privatev1.TemplateParameterPolicy_builder{Locked: value}.Build()}
	resolved, err := applyCatalogItemTemplateParameterPolicies(template, locked, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(resolved["enabled"], value) {
		t.Fatal("locked false value was not preserved")
	}
	resolved["enabled"].Value = []byte{42}
	if proto.Equal(resolved["enabled"], value) {
		t.Fatal("policy payload aliases the materialized value")
	}
	if _, err := applyCatalogItemTemplateParameterPolicies(template, locked, map[string]*anypb.Any{"enabled": value}); err == nil {
		t.Fatal("locked input was accepted")
	}
	editable := map[string]*privatev1.TemplateParameterPolicy{"enabled": privatev1.TemplateParameterPolicy_builder{Editable: privatev1.EditableTemplateParameter_builder{DefaultValue: value}.Build()}.Build()}
	supplied, err := anypb.New(wrapperspb.Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = applyCatalogItemTemplateParameterPolicies(template, editable, map[string]*anypb.Any{"enabled": supplied})
	if err != nil || !proto.Equal(resolved["enabled"], supplied) {
		t.Fatal("editable input did not override the default", err)
	}
	for _, bad := range []*anypb.Any{{TypeUrl: value.GetTypeUrl(), Value: []byte{0xff}}, {TypeUrl: "type.googleapis.com/google.protobuf.StringValue"}} {
		if err := validateCatalogItemTemplateParameterPolicies(template, map[string]*privatev1.TemplateParameterPolicy{"enabled": privatev1.TemplateParameterPolicy_builder{Locked: bad}.Build()}); err == nil {
			t.Fatal("invalid Any was accepted")
		}
	}
	if err := validateCatalogItemTemplateParameterPolicies(template, map[string]*privatev1.TemplateParameterPolicy{"unknown": locked["enabled"]}); err == nil {
		t.Fatal("unknown parameter was accepted")
	}
}

func TestCatalogItemParameterTypes(t *testing.T) {
	values := map[string]proto.Message{
		"boolean": wrapperspb.Bool(false), "int32": wrapperspb.Int32(0), "int64": wrapperspb.Int64(0),
		"float": wrapperspb.Float(0), "double": wrapperspb.Double(0), "string": wrapperspb.String(""),
		"bytes": wrapperspb.Bytes(nil), "timestamp": &timestamppb.Timestamp{Seconds: 123}, "duration": &durationpb.Duration{Seconds: 3},
	}
	for name, message := range values {
		t.Run(name, func(t *testing.T) {
			value, err := anypb.New(message)
			if err != nil {
				t.Fatal(err)
			}
			template := utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: privatev1.ComputeInstanceTemplate_builder{
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: name, Type: value.GetTypeUrl()}.Build()},
			}.Build()}
			policies := map[string]*privatev1.TemplateParameterPolicy{name: privatev1.TemplateParameterPolicy_builder{Locked: value}.Build()}
			if err := validateCatalogItemTemplateParameterPolicies(template, policies); err != nil {
				t.Fatal(err)
			}
			resolved, err := applyCatalogItemTemplateParameterPolicies(template, policies, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(resolved[name], value) {
				t.Fatal("parameter value changed")
			}
		})
	}
}

func TestCatalogItemInvalidParameterValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value proto.Message
	}{
		{"invalid timestamp", &timestamppb.Timestamp{Seconds: 253402300800}},
		{"invalid duration", &durationpb.Duration{Seconds: 1, Nanos: -1}},
		{"structured governance", structpb.NewStringValue("value")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := anypb.New(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			template := utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: privatev1.ComputeInstanceTemplate_builder{
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: "parameter", Type: value.GetTypeUrl()}.Build()},
			}.Build()}
			policy := map[string]*privatev1.TemplateParameterPolicy{"parameter": privatev1.TemplateParameterPolicy_builder{Locked: value}.Build()}
			if err := validateCatalogItemTemplateParameterPolicies(template, policy); err == nil {
				t.Fatal("invalid policy was accepted")
			}
		})
	}
}
