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

package netboxclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

const testToken = "test-netbox-api-token"

var _ = Describe("NetBox client", func() {
	Describe("endpoint normalization", func() {
		DescribeTable("normalizes or rejects endpoints",
			func(endpoint string, allowInsecureHTTP bool, want string, wantErr bool) {
				got, err := normalizeEndpoint(endpoint, allowInsecureHTTP)
				if wantErr {
					Expect(err).To(HaveOccurred())
					return
				}

				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(want))
			},
			Entry("origin", "https://netbox.example.test", false, "https://netbox.example.test", false),
			Entry("trailing slash", "https://netbox.example.test/", false, "https://netbox.example.test", false),
			Entry("trailing slash and whitespace", " https://netbox.example.test/ ", false, "https://netbox.example.test", false),
			Entry("loopback IPv4 HTTP requires opt-in", "http://127.0.0.1:8080", false, "", true),
			Entry("loopback IPv4 HTTP with opt-in", "http://127.0.0.1:8080", true, "http://127.0.0.1:8080", false),
			Entry("loopback IPv6 HTTP requires opt-in", "http://[::1]:8080", false, "", true),
			Entry("loopback IPv6 HTTP with opt-in", "http://[::1]:8080", true, "http://[::1]:8080", false),
			Entry("localhost HTTP requires opt-in", "http://localhost:8080", false, "", true),
			Entry("localhost HTTP with opt-in", "http://localhost:8080", true, "http://localhost:8080", false),
			Entry("remote HTTP requires opt-in", "http://netbox.example.test", false, "", true),
			Entry("remote HTTP with opt-in", "http://netbox.example.test", true, "http://netbox.example.test", false),
			Entry("localhost subdomain HTTP requires opt-in", "http://netbox.localhost", false, "", true),
			Entry("unsupported scheme is rejected", "ftp://netbox.example.test", false, "", true),
			Entry("API path is rejected", "https://netbox.example.test/api", false, "", true),
			Entry("other path is rejected", "https://netbox.example.test/netbox", false, "", true),
			Entry("credentials are rejected", "https://user:password@netbox.example.test", false, "", true),
			Entry("query is rejected", "https://netbox.example.test?token=secret", false, "", true),
			Entry("fragment is rejected", "https://netbox.example.test/#devices", false, "", true),
			Entry("missing host is rejected", "https:///api", false, "", true),
			Entry("malformed URL is rejected", "not a URL", false, "", true),
		)
	})

	Describe("token reading", func() {
		DescribeTable("trims valid tokens and rejects malformed tokens",
			func(content, want string, wantErr bool) {
				path := filepath.Join(GinkgoT().TempDir(), "token")
				Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())

				got, err := readToken(path)
				if wantErr {
					Expect(err).To(HaveOccurred())
					return
				}

				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(want))
			},
			Entry("trims mounted file newline", testToken+"\n", testToken, false),
			Entry("trims surrounding whitespace", " \t"+testToken+" \n", testToken, false),
			Entry("empty token", " \n", "", true),
			Entry("embedded newline", "first\nsecond", "", true),
			Entry("embedded space", "first second", "", true),
		)

		It("rejects a missing token file", func() {
			_, err := readToken(filepath.Join(GinkgoT().TempDir(), "missing"))
			Expect(errors.Is(err, errInvalidToken)).To(BeTrue())
		})

		It("rejects an empty token path", func() {
			_, err := readToken("")
			Expect(errors.Is(err, errInvalidToken)).To(BeTrue())
		})
	})

	It("rejects an invalid CA certificate", func() {
		tokenFile := writeTestToken()
		caFile := filepath.Join(GinkgoT().TempDir(), "ca.pem")
		Expect(os.WriteFile(caFile, []byte("not a certificate"), 0o600)).To(Succeed())

		_, err := New(Config{Endpoint: "https://netbox.example.test", TokenFile: tokenFile, CAFile: caFile})
		Expect(errors.Is(err, errInvalidCA)).To(BeTrue())
	})

	Describe("client configuration", func() {
		DescribeTable("validates HTTP and token-file requirements",
			func(endpoint string, allowInsecureHTTP, withToken bool, want error) {
				config := Config{Endpoint: endpoint, AllowInsecureHTTP: allowInsecureHTTP}
				if withToken {
					config.TokenFile = writeTestToken()
				}

				_, err := New(config)
				if want == nil {
					Expect(err).NotTo(HaveOccurred())
					return
				}
				Expect(errors.Is(err, want)).To(BeTrue())
			},
			Entry("loopback HTTP endpoint requires opt-in", "http://127.0.0.1:8080", false, true, errInsecureHTTPNotAllowed),
			Entry("loopback HTTP endpoint with opt-in", "http://127.0.0.1:8080", true, true, nil),
			Entry("remote HTTP endpoint requires opt-in", "http://netbox.example.test", false, true, errInsecureHTTPNotAllowed),
			Entry("remote HTTP endpoint with opt-in", "http://netbox.example.test", true, true, nil),
			Entry("missing token file", "https://netbox.example.test", false, false, errInvalidToken),
		)
	})

	It("returns a typed device and ETag", func() {
		etag := `W/"device-revision-42"`
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodGet))
			Expect(r.URL.Path).To(Equal("/api/dcim/devices/42/"))
			Expect(r.Header.Get("Authorization")).To(Equal("Token " + testToken))
			w.Header().Set("ETag", etag)
			writeJSON(w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
		}))

		snapshot, err := client.GetDevice(context.Background(), "42")
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.ETag).To(Equal(etag))
		Expect(snapshot.Device.ID).To(Equal("42"))
		Expect(snapshot.Device.Name).To(Equal("host-42"))
		Expect(snapshot.Device.Status).To(Equal("active"))
		Expect(snapshot.Device.CustomFields["osac_instance_id"]).To(Equal("instance-42"))
	})

	It("encodes status and custom-field filters for device queries", func() {
		longFieldName := strings.Repeat("x", 51)
		freeDevice := strings.Replace(
			deviceResponse(42, "host-42", "staged"),
			`"custom_fields": {"osac_instance_id": "instance-42"}`,
			`"custom_fields": {"osac_managed": true, "osac_instance_id": ""}`,
			1,
		)
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodGet))
			Expect(r.URL.Path).To(Equal("/api/dcim/devices/"))
			query := r.URL.Query()
			Expect(query.Get("status")).To(Equal("staged"))
			Expect(query.Get("cf_osac_managed")).To(Equal("true"))
			Expect(query.Get("cf_osac_instance_id__empty")).To(Equal("true"))
			_, exists := query["cf_osac_instance_id"]
			Expect(exists).To(BeFalse())
			Expect(query.Get("cf_gpu-model")).To(Equal("a100"))
			Expect(query.Get("cf_" + longFieldName)).To(Equal("long-name"))
			Expect(query.Get("limit")).To(Equal("100"))
			Expect(query.Get("offset")).To(Equal("0"))
			Expect(r.Header.Get("Authorization")).To(Equal("Token " + testToken))
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    1,
				"next":     nil,
				"previous": nil,
				"results":  []json.RawMessage{json.RawMessage(freeDevice)},
			})
		}))

		var devices []Device
		err := client.ForEachDevice(context.Background(), DeviceQuery{
			"status":                     "staged",
			"cf_osac_managed":            "true",
			"cf_osac_instance_id__empty": "true",
			"cf_gpu-model":               "a100",
			"cf_" + longFieldName:        "long-name",
		}, func(device Device) (bool, error) {
			devices = append(devices, device)
			return true, nil
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(devices).To(HaveLen(1))
		Expect(devices[0].ID).To(Equal("42"))
		Expect(devices[0].Name).To(Equal("host-42"))
	})

	It("escapes custom-field query values", func() {
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()
			Expect(query["status"]).To(Equal([]string{"staged"}))
			Expect(query.Get("cf_placement_zone")).To(Equal("&status=active"))
			Expect(query.Get("cf_model")).To(Equal("a+b # 100%"))
			writeJSON(w, http.StatusOK, map[string]any{
				"count": 0, "next": nil, "previous": nil, "results": []json.RawMessage{},
			})
		}))

		err := client.ForEachDevice(context.Background(), DeviceQuery{
			"status":            "staged",
			"cf_placement_zone": "&status=active",
			"cf_model":          "a+b # 100%",
		}, func(Device) (bool, error) {
			Fail("visitor called for an empty result set")
			return false, nil
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("follows device pagination", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := calls.Add(1)
			expectedOffset := "0"
			if call == 2 {
				expectedOffset = "100"
			}
			Expect(r.URL.Query().Get("offset")).To(Equal(expectedOffset))
			if call == 1 {
				results := make([]json.RawMessage, int(defaultPageSize))
				for i := range results {
					results[i] = json.RawMessage(deviceResponse(1000+i, fmt.Sprintf("host-%d", 1000+i), "active"))
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"count":    101,
					"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=100",
					"previous": nil,
					"results":  results,
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    101,
				"next":     nil,
				"previous": "https://netbox.example.test/api/dcim/devices/?limit=100&offset=0",
				"results":  []json.RawMessage{json.RawMessage(deviceResponse(1100, "host-1100", "active"))},
			})
		}))

		var devices []Device
		err := client.ForEachDevice(context.Background(), nil, func(device Device) (bool, error) {
			devices = append(devices, device)
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(2)))
		Expect(devices).To(HaveLen(101))
		Expect(devices[0].ID).To(Equal("1000"))
		Expect(devices[100].ID).To(Equal("1100"))
	})

	It("stops device pagination when the visitor stops", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    201,
				"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=100",
				"previous": nil,
				"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
			})
		}))

		var visited int
		err := client.ForEachDevice(context.Background(), nil, func(Device) (bool, error) {
			visited++
			return false, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(visited).To(Equal(1))
	})

	It("stops device pagination when the context is canceled", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    201,
				"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=100",
				"previous": nil,
				"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
			})
		}))

		ctx, cancel := context.WithCancel(context.Background())
		err := client.ForEachDevice(ctx, nil, func(Device) (bool, error) {
			cancel()
			return true, nil
		})
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("rejects a next page beyond the reported device count", func() {
		var calls atomic.Int32
		var visited atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    1,
				"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=1",
				"previous": nil,
				"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
			})
		}))

		err := client.ForEachDevice(context.Background(), nil, func(Device) (bool, error) {
			visited.Add(1)
			return true, nil
		})
		expectAPIError(err, ErrorKindInvalidResponse, 0)
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(visited.Load()).To(Equal(int32(0)))
	})

	It("rejects unsupported device queries without making a request", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusOK)
		}))

		err := client.ForEachDevice(context.Background(), DeviceQuery{"name": "host-42"}, func(Device) (bool, error) {
			return true, nil
		})
		expectAPIError(err, ErrorKindInvalidRequest, 0)
		Expect(calls.Load()).To(Equal(int32(0)))
	})

	It("requests device custom fields by device object type", func() {
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodGet))
			Expect(r.URL.Path).To(Equal("/api/extras/custom-fields/"))
			Expect(r.URL.Query().Get("object_type")).To(Equal("dcim.device"))
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    1,
				"next":     nil,
				"previous": nil,
				"results": []any{map[string]any{
					"id":           7,
					"url":          "/api/extras/custom-fields/7/",
					"display":      "OSAC instance ID",
					"object_types": []string{"dcim.device"},
					"type":         map[string]string{"value": "text", "label": "Text"},
					"data_type":    "text",
					"name":         "osac_instance_id",
					"label":        "OSAC instance ID",
					"required":     false,
				}},
			})
		}))

		var fields []CustomField
		err := client.ForEachDeviceCustomField(context.Background(), func(field CustomField) (bool, error) {
			fields = append(fields, field)
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fields).To(HaveLen(1))
		field := fields[0]
		Expect(field.Name).To(Equal("osac_instance_id"))
		Expect(field.Label).To(Equal("OSAC instance ID"))
		Expect(field.Type).To(Equal("text"))
		Expect(field.DataType).To(Equal("text"))
		Expect(field.Required).To(BeFalse())
		Expect(field.ObjectTypes).To(Equal([]string{"dcim.device"}))
	})

	It("follows custom-field pagination", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Query().Get("object_type")).To(Equal("dcim.device"))
			Expect(r.URL.Query().Get("limit")).To(Equal("100"))

			calls.Add(1)
			switch r.URL.Query().Get("offset") {
			case "0":
				results := make([]any, 0, 100)
				for id := 1; id <= 100; id++ {
					results = append(results, customFieldResult(id, fmt.Sprintf("field-%d", id)))
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"count":    101,
					"next":     "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=100",
					"previous": nil,
					"results":  results,
				})
			case "100":
				writeJSON(w, http.StatusOK, map[string]any{
					"count":    101,
					"next":     nil,
					"previous": "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=0",
					"results":  []any{customFieldResult(101, "field-101")},
				})
			default:
				Fail(fmt.Sprintf("offset = %q, want 0 or 100", r.URL.Query().Get("offset")))
				w.WriteHeader(http.StatusBadRequest)
			}
		}))

		var names []string
		err := client.ForEachDeviceCustomField(context.Background(), func(field CustomField) (bool, error) {
			names = append(names, field.Name)
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(2)))
		Expect(names).To(HaveLen(101))
		Expect(names[0]).To(Equal("field-1"))
		Expect(names[100]).To(Equal("field-101"))
	})

	It("stops custom-field pagination when the visitor stops", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			results := make([]any, 0, 100)
			for id := 1; id <= 100; id++ {
				results = append(results, customFieldResult(id, fmt.Sprintf("field-%d", id)))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    101,
				"next":     "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=100",
				"previous": nil,
				"results":  results,
			})
		}))

		var visited int
		err := client.ForEachDeviceCustomField(context.Background(), func(CustomField) (bool, error) {
			visited++
			return false, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(visited).To(Equal(1))
	})

	It("rejects a next custom-field page beyond the reported count", func() {
		var visited atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"count":    1,
				"next":     "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=1",
				"previous": nil,
				"results": []any{map[string]any{
					"id":           7,
					"url":          "/api/extras/custom-fields/7/",
					"display":      "OSAC instance ID",
					"object_types": []string{"dcim.device"},
					"type":         map[string]string{"value": "text", "label": "Text"},
					"data_type":    "text",
					"name":         "osac_instance_id",
					"label":        "OSAC instance ID",
					"required":     false,
				}},
			})
		}))

		err := client.ForEachDeviceCustomField(context.Background(), func(CustomField) (bool, error) {
			visited.Add(1)
			return true, nil
		})
		expectAPIError(err, ErrorKindInvalidResponse, 0)
		Expect(visited.Load()).To(Equal(int32(0)))
	})

	DescribeTable("rejects invalid pagination metadata",
		func(offset, total int32, resultCount int, hasNext bool) {
			_, err := nextPaginationOffset(offset, total, resultCount, hasNext)
			expectAPIError(err, ErrorKindInvalidResponse, 0)
		},
		Entry("negative offset", int32(-1), int32(1), 1, false),
		Entry("negative count", int32(0), int32(-1), 1, false),
		Entry("page exceeds requested limit", int32(0), int32(101), 101, true),
		Entry("empty page with next", int32(0), int32(1), 0, true),
		Entry("offset exceeds SDK range", int32(maxInt32Offset)-50, int32(maxInt32Offset), 100, true),
	)

	It("forwards ETag and returns the updated device snapshot", func() {
		const expectedETag = `W/"device-revision-42"`
		const updatedETag = `W/"device-revision-43"`
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			Expect(r.Method).To(Equal(http.MethodPatch))
			Expect(r.URL.Path).To(Equal("/api/dcim/devices/42/"))
			Expect(r.Header.Get("If-Match")).To(Equal(expectedETag))
			Expect(r.Header.Get("Authorization")).To(Equal("Token " + testToken))

			var body map[string]any
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			Expect(body["status"]).To(Equal("active"))
			customFields, _ := body["custom_fields"].(map[string]any)
			Expect(customFields["osac_instance_id"]).To(Equal("instance-42"))
			Expect(body["changelog_message"]).To(Equal("Claimed by OSAC"))

			w.Header().Set("ETag", updatedETag)
			writeJSON(w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
		}))

		status := "active"
		snapshot, err := client.PatchDevice(context.Background(), "42", DevicePatch{
			Status:           &status,
			CustomFields:     map[string]any{"osac_instance_id": "instance-42"},
			ChangelogMessage: "Claimed by OSAC",
		}, expectedETag)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.ETag).To(Equal(updatedETag))
		Expect(snapshot.Device.ID).To(Equal("42"))
		Expect(snapshot.Device.Status).To(Equal("active"))
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("rejects a missing ETag without making a request", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		status := "active"

		_, err := client.PatchDevice(context.Background(), "42", DevicePatch{Status: &status}, " \r\n")
		expectAPIError(err, ErrorKindInvalidETag, 0)
		Expect(calls.Load()).To(Equal(int32(0)))
	})

	It("retries transient GetDevice failures and sanitizes errors", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"detail": "sensitive response body",
				})
				return
			}
			writeJSON(w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
		}))

		snapshot, err := client.GetDevice(context.Background(), "42")
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Device.ID).To(Equal("42"))
		Expect(calls.Load()).To(Equal(int32(2)))
	})

	DescribeTable("classifies and redacts permanent GetDevice failures",
		func(statusCode int, wantKind ErrorKind) {
			var calls atomic.Int32
			server, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(statusCode)
				_, _ = fmt.Fprintf(w, "private response body; token=%s", testToken)
			}))

			_, err := client.GetDevice(context.Background(), "42")
			expectAPIError(err, wantKind, statusCode)
			Expect(calls.Load()).To(Equal(int32(1)))
			expectDoesNotContain(err.Error(), testToken, "private response body", server.URL)
		},
		Entry("unauthorized", http.StatusUnauthorized, ErrorKindUnauthorized),
		Entry("forbidden", http.StatusForbidden, ErrorKindForbidden),
		Entry("not found", http.StatusNotFound, ErrorKindNotFound),
	)

	It("stops after three transient GetDevice failures", func() {
		var calls atomic.Int32
		server, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "private response body; token=%s", testToken)
		}))

		_, err := client.GetDevice(context.Background(), "42")
		expectAPIError(err, ErrorKindUnavailable, http.StatusServiceUnavailable)
		Expect(calls.Load()).To(Equal(int32(maxGETAttempts)))
		expectDoesNotContain(err.Error(), testToken, "private response body", server.URL)
	})

	It("does not retry a PatchDevice failure with an uncertain outcome", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			Expect(r.Header.Get("If-Match")).To(Equal(`W/"device-revision-42"`))
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "private response body; token=%s", testToken)
		}))
		status := "active"

		_, err := client.PatchDevice(context.Background(), "42", DevicePatch{Status: &status}, `W/"device-revision-42"`)
		expectAPIError(err, ErrorKindUnavailable, http.StatusServiceUnavailable)
		Expect(calls.Load()).To(Equal(int32(1)))
		expectDoesNotContain(err.Error(), testToken, "private response body")
	})

	It("classifies PatchDevice precondition failures", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			Expect(r.Header.Get("If-Match")).To(Equal(`W/"old-revision"`))
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte("stale ETag detail"))
		}))
		status := "active"

		_, err := client.PatchDevice(context.Background(), "42", DevicePatch{Status: &status}, `W/"old-revision"`)
		expectAPIError(err, ErrorKindConflict, http.StatusPreconditionFailed)
		Expect(calls.Load()).To(Equal(int32(1)))
		expectDoesNotContain(err.Error(), "stale ETag detail")
	})

	It("rejects a TLS certificate outside the configured roots", func() {
		var calls atomic.Int32
		server := httptest.NewUnstartedServer(ginkgoRequestHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
		})))
		server.Config.ErrorLog = log.New(io.Discard, "", 0)
		server.StartTLS()
		DeferCleanup(server.Close)

		client, err := New(Config{Endpoint: server.URL, TokenFile: writeTestToken()})
		Expect(err).NotTo(HaveOccurred())

		_, err = client.GetDevice(context.Background(), "42")
		expectAPIError(err, ErrorKindTransport, 0)
		Expect(calls.Load()).To(Equal(int32(0)))
		expectDoesNotContain(err.Error(), server.URL, testToken)
	})

	It("does not follow redirects", func() {
		var destinationCalls atomic.Int32
		destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			destinationCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(destination.Close)

		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, destination.URL+"/api/dcim/devices/42/", http.StatusFound)
		}))
		_, err := client.GetDevice(context.Background(), "42")
		expectAPIError(err, ErrorKindRedirect, http.StatusFound)
		Expect(destinationCalls.Load()).To(Equal(int32(0)))
	})

	It("rejects non-numeric device IDs before making a request", func() {
		var calls atomic.Int32
		_, client := newTrustedClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusOK)
		}))

		_, err := client.GetDevice(context.Background(), "42/other")
		expectAPIError(err, ErrorKindInvalidRequest, 0)
		Expect(calls.Load()).To(Equal(int32(0)))
	})

	It("keeps TLS certificate verification enabled", func() {
		config, err := newTLSConfig("")
		Expect(err).NotTo(HaveOccurred())
		Expect(config.InsecureSkipVerify).To(BeFalse())
		Expect(config.MinVersion).To(BeNumerically(">=", tls.VersionTLS12))
		Expect(config.RootCAs).NotTo(BeNil())
	})

	DescribeTable("rejects overflowing and non-positive device IDs",
		func(id string) {
			_, err := parseDeviceID(id)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty ID", ""),
		Entry("zero", "0"),
		Entry("negative ID", "-1"),
		Entry("integer overflow", "2147483648"),
		Entry("non-numeric ID", "host-42"),
		Entry("ID with a path separator", "42/"),
	)
})

func newTrustedClient(handler http.Handler) (*httptest.Server, *Client) {
	server := httptest.NewTLSServer(ginkgoRequestHandler(handler))
	DeferCleanup(server.Close)

	caFile := filepath.Join(GinkgoT().TempDir(), "netbox-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	Expect(os.WriteFile(caFile, caPEM, 0o600)).To(Succeed())

	client, err := New(Config{Endpoint: server.URL + "/", TokenFile: writeTestToken(), CAFile: caFile})
	Expect(err).NotTo(HaveOccurred())
	return server, client
}

func ginkgoRequestHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		handler.ServeHTTP(w, r)
	})
}

func writeTestToken() string {
	path := filepath.Join(GinkgoT().TempDir(), "netbox-token")
	Expect(os.WriteFile(path, []byte(testToken+"\n"), 0o600)).To(Succeed())
	return path
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	Expect(json.NewEncoder(w).Encode(value)).To(Succeed())
}

func deviceResponse(id int, name, status string) string {
	return fmt.Sprintf(`{
		"id": %d,
		"url": "/api/dcim/devices/%d/",
		"display": %q,
		"name": %q,
		"device_type": {
			"id": 1,
			"url": "/api/dcim/device-types/1/",
			"display": "Test device type",
			"manufacturer": {"id": 1, "url": "/api/dcim/manufacturers/1/", "display": "Test manufacturer", "name": "Test manufacturer", "slug": "test-manufacturer"},
			"model": "Test model",
			"slug": "test-device-type"
		},
		"role": {"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server", "name": "Server", "slug": "server", "_depth": 0},
		"site": {"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site", "name": "Test site", "slug": "test-site"},
		"status": {"value": %q, "label": "Active"},
		"custom_fields": {"osac_instance_id": "instance-42"},
		"console_port_count": 0,
		"console_server_port_count": 0,
		"power_port_count": 0,
		"power_outlet_count": 0,
		"front_port_count": 0,
		"rear_port_count": 0,
		"device_bay_count": 0,
		"module_bay_count": 0,
		"inventory_item_count": 0
	}`, id, id, name, name, status)
}

func customFieldResult(id int, name string) map[string]any {
	label := "Custom field " + name
	return map[string]any{
		"id":           id,
		"url":          fmt.Sprintf("/api/extras/custom-fields/%d/", id),
		"display":      label,
		"object_types": []string{"dcim.device"},
		"type":         map[string]string{"value": "text", "label": "Text"},
		"data_type":    "text",
		"name":         name,
		"label":        label,
		"required":     false,
	}
}

func expectAPIError(err error, wantKind ErrorKind, wantStatus int) {
	Expect(err).To(HaveOccurred())
	var apiErr *APIError
	Expect(errors.As(err, &apiErr)).To(BeTrue())
	Expect(apiErr.Kind).To(Equal(wantKind))
	Expect(apiErr.StatusCode).To(Equal(wantStatus))
}

func expectDoesNotContain(value string, forbidden ...string) {
	for _, part := range forbidden {
		if part != "" {
			Expect(value).NotTo(ContainSubstring(part))
		}
	}
}
