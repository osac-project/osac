/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
)

// billingRateCardScenario uses a clean QUANTITY card: mixed unsupported rows are a separate adapter concern.
func billingRateCardScenario(instanceTypeName string) billingfake.Scenario {
	return billingfake.Scenario{Services: []billingfake.ServiceSpec{{
		ID: "billing-it-compute", Name: "compute_instance",
		Attributes: []billing.PriceableAttribute{{ID: "billing-it-instance-type", Name: "instance_type"}},
		Rates: map[string]billingfake.RateCardSpec{"USD": {Rows: []billing.RateRow{{
			DimensionID: "billing-it-instance-type", PriceMethod: "QUANTITY", UnitRate: "0.00125",
			AttributeValues: []billing.AttributeValue{{Name: "instance_type", Value: instanceTypeName}},
		}}}},
	}}}
}

// expectBillingPricingCountOnlyCLI checks count-only output without depending on table alignment or serialization
// whitespace. A list (including an empty list) is not a count-only response.
func expectBillingPricingCountOnlyCLI(g Gomega, stdout, format string, total int32) {
	if format == "table" {
		g.Expect(strings.Fields(stdout)).To(Equal([]string{"TOTAL", strconv.FormatInt(int64(total), 10)}))
		return
	}
	var object map[string]int32
	// YAML decoding also accepts JSON, but keep JSON strict so YAML cannot accidentally pass as JSON output.
	if format == "json" {
		g.Expect(json.Unmarshal([]byte(stdout), &object)).To(Succeed(), "stdout: %s", stdout)
	} else {
		g.Expect(yaml.UnmarshalStrict([]byte(stdout), &object)).To(Succeed(), "stdout: %s", stdout)
	}
	g.Expect(object).To(Equal(map[string]int32{"total": total}))
}

// TestBillingRateCardScenario checks the deployed fixture's protocol shape without a cluster. This is not deployed
// integration evidence: it runs only the real adapter and normalizer against the loopback HTTPS stub.
func TestBillingRateCardScenario(t *testing.T) {
	g := NewWithT(t)
	stub := billingstub.New(billingRateCardScenario("billing-it-small"))
	ca, err := stub.StartTLS("127.0.0.1:0", "127.0.0.1")
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(stub.Close)
	roots := x509.NewCertPool()
	g.Expect(roots.AppendCertsFromPEM(ca)).To(BeTrue())
	adapter, err := m360.New(billing.Config{
		BaseURL: stub.URL() + "/api/v1/external", Credential: "stub-token", RootCAs: roots,
	}).Build()
	g.Expect(err).ToNot(HaveOccurred())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, skipped, err := pricing.NewResolver(adapter).ListServiceRateCardItems(ctx, "compute_instance", "USD")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(skipped).To(BeEmpty())
	g.Expect(items).To(HaveLen(1))
	g.Expect(items[0].Service).To(Equal("compute_instance"))
	g.Expect(items[0].Currency).To(Equal("USD"))
	g.Expect(items[0].Amount.String()).To(Equal("0.00125"))
	g.Expect(items[0].Unit).To(Equal("second"))
	g.Expect(items[0].Conditions).To(Equal([]pricing.RateCondition{{
		Dimension: "instance_type", Operator: pricing.OperatorEquals, Value: "billing-it-small",
	}}))
	g.Expect(stub.Requests()).To(HaveLen(3))
	for _, op := range []string{"FindServiceByName", "ListPriceableAttributes", "GetServiceRateCard"} {
		g.Expect(stub.Succeeded(op)).To(Equal(1), op)
	}
	g.Expect(stub.Requests()).To(HaveEach(HaveField("Method", http.MethodGet)))
	g.Expect(stub.Writes()).To(BeEmpty())
}
