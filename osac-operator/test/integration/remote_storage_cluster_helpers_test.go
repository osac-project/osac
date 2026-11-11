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

package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// TestCSPFixturePodNetworkingMode checks that either installed networking mode
// reaches the fixture process through the rendered Pod command.
func TestCSPFixturePodNetworkingMode(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			var pod corev1.Pod
			if err := yaml.Unmarshal([]byte(cspFixturePodYAML("fixture:test", enabled)), &pod); err != nil {
				t.Fatal(err)
			}
			if len(pod.Spec.Containers) != 1 {
				t.Fatalf("expected one fixture container, got %d", len(pod.Spec.Containers))
			}
			command := strings.Join(pod.Spec.Containers[0].Command, " ")
			want := "--networking-provisioning-enabled=" + strconv.FormatBool(enabled)
			if !strings.Contains(command, want) {
				t.Fatalf("fixture command does not pass %s", want)
			}
		})
	}
}

// TestOperatorRestartEvidenceOSAC7223 rejects stale process and credential evidence.
func TestOperatorRestartEvidenceOSAC7223(t *testing.T) {
	identity := "system:serviceaccount:osac:osac-remote-no-storage"
	forbidden := fmt.Sprintf(
		`storageclasses.storage.k8s.io is forbidden: User %q cannot list resource "storageclasses" at the cluster scope`,
		identity,
	)
	jsonLog, err := json.Marshal(map[string]string{"error": forbidden})
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name          string
		mutate        func(initial, current *corev1.Pod)
		logs          string
		identity      string
		readError     bool
		logError      bool
		confirmChange bool
		wantSuccess   bool
	}{
		{name: "current evidence without previous logs", logs: forbidden, wantSuccess: true},
		{name: "JSON current evidence without previous logs", logs: string(jsonLog), wantSuccess: true},
		{name: "unchanged process", mutate: func(initial, current *corev1.Pod) { *current = *initial.DeepCopy() }},
		{name: "unchanged container ID", mutate: func(initial, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].ContainerID = initial.Status.ContainerStatuses[0].ContainerID
		}},
		{name: "unchanged restart count", mutate: func(initial, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].RestartCount = initial.Status.ContainerStatuses[0].RestartCount
		}},
		{name: "decreased restart count", mutate: func(initial, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].RestartCount = initial.Status.ContainerStatuses[0].RestartCount - 1
		}},
		{name: "replacement pod", mutate: func(_, current *corev1.Pod) { current.UID = "replacement" }},
		{name: "missing baseline UID", mutate: func(initial, _ *corev1.Pod) { initial.UID = "" }},
		{name: "missing baseline container ID", mutate: func(initial, _ *corev1.Pod) {
			initial.Status.ContainerStatuses[0].ContainerID = ""
		}},
		{name: "missing current container ID", mutate: func(_, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].ContainerID = ""
		}},
		{name: "missing baseline manager", mutate: func(initial, _ *corev1.Pod) {
			initial.Status.ContainerStatuses = nil
		}},
		{name: "missing current manager", mutate: func(_, current *corev1.Pod) {
			current.Status.ContainerStatuses = nil
		}},
		{name: "unready manager", mutate: func(_, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].Ready = false
		}},
		{name: "waiting manager", mutate: func(_, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].State = corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
			}
		}},
		{name: "terminated manager", mutate: func(_, current *corev1.Pod) {
			current.Status.ContainerStatuses[0].State = corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
			}
		}},
		{name: "pending pod", mutate: func(_, current *corev1.Pod) { current.Status.Phase = corev1.PodPending }},
		{name: "terminating pod", mutate: func(_, current *corev1.Pod) {
			timestamp := metav1.Now()
			current.DeletionTimestamp = &timestamp
		}},
		{name: "generic Forbidden", logs: "storageclasses.storage.k8s.io is forbidden"},
		{name: "wrong credential identity",
			logs: strings.ReplaceAll(forbidden, identity, "system:serviceaccount:osac:other")},
		{name: "credential identity suffix", logs: strings.ReplaceAll(forbidden, identity, identity+"-other")},
		{name: "unrelated identity on another line", logs: "authenticated as " + identity + "\n" +
			strings.ReplaceAll(forbidden, identity, "system:serviceaccount:osac:other")},
		{name: "wrong forbidden resource", logs: strings.ReplaceAll(forbidden, "storageclasses", "namespaces")},
		{name: "previous message only", logs: "remote cluster kubeconfig changed; restarting operator"},
		{name: "wrong expected identity", logs: forbidden, identity: identity + "-other"},
		{name: "pod read fails", readError: true},
		{name: "current logs unavailable", logError: true},
		{name: "process changes while reading logs", confirmChange: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			initial := operatorRestartTestPod()
			current := initial.DeepCopy()
			current.Status.ContainerStatuses[0].ContainerID = "containerd://new-manager"
			current.Status.ContainerStatuses[0].RestartCount++
			if testCase.mutate != nil {
				testCase.mutate(&initial, current)
			}
			logs := testCase.logs
			if logs == "" {
				logs = forbidden
			}
			expectedIdentity := identity
			if testCase.identity != "" {
				expectedIdentity = testCase.identity
			}
			podReads, previousLogReads := 0, 0
			kubectl := func(args ...string) ([]byte, error) {
				switch args[0] {
				case "get":
					if strings.Join(args, " ") != "get pod operator-pod -n osac -o json" {
						t.Fatalf("unexpected status command: %v", args)
					}
					podReads++
					if testCase.readError {
						return nil, errors.New("pod unavailable: do-not-print-command-output")
					}
					if podReads > 1 && testCase.confirmChange {
						current.Status.ContainerStatuses[0].ContainerID = "containerd://another-manager"
						current.Status.ContainerStatuses[0].RestartCount++
					}
					return json.Marshal(current)
				case "logs":
					if strings.Contains(strings.Join(args, " "), "--previous") {
						previousLogReads++
						return nil, errors.New("previous terminated container not found")
					}
					if strings.Join(args, " ") != "logs operator-pod -n osac -c manager --tail=300" {
						t.Fatalf("unexpected current log command: %v", args)
					}
					if testCase.logError {
						return nil, errors.New("current logs unavailable: do-not-print-command-output")
					}
					return []byte(logs), nil
				default:
					t.Fatalf("unexpected kubectl command: %v", args)
					return nil, nil
				}
			}
			err := verifyOperatorRestart(initial, expectedIdentity, kubectl)
			if (err == nil) != testCase.wantSuccess {
				t.Fatalf("verification error = %v, want success = %t", err, testCase.wantSuccess)
			}
			if err != nil && strings.Contains(err.Error(), "do-not-print") {
				t.Fatal("verification exposed command output")
			}
			if previousLogReads != 0 {
				t.Fatal("previous logs must not participate in restart verification")
			}
			if testCase.wantSuccess && podReads != 2 {
				t.Fatalf("expected status checks before and after current logs, got %d", podReads)
			}
		})
	}
}

func TestOperatorRestartDiagnosticsOSAC7223(t *testing.T) {
	for _, previousAvailable := range []bool{false, true} {
		t.Run(strconv.FormatBool(previousAvailable), func(t *testing.T) {
			pod := operatorRestartTestPod()
			pod.Spec.Containers = []corev1.Container{{Name: "manager", Env: []corev1.EnvVar{
				{Name: "SENSITIVE_FIXTURE", Value: "do-not-print-spec"},
			}}}
			pod.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
				ContainerID: "containerd://terminated-manager", Reason: "Error", ExitCode: 1,
				Message: "do-not-print-termination-message",
			}
			previousLogReads := 0
			kubectl := func(args ...string) ([]byte, error) {
				if args[0] == "get" {
					return json.Marshal(pod)
				}
				if strings.Join(args, " ") != "logs operator-pod -n osac -c manager --previous --tail=100" {
					t.Fatalf("unexpected diagnostics command: %v", args)
				}
				previousLogReads++
				if !previousAvailable {
					return nil, errors.New("previous logs unavailable")
				}
				return []byte("do-not-print-log-content\nremote cluster kubeconfig changed; restarting operator"), nil
			}
			diagnostics := operatorRestartDiagnostics(pod, kubectl)
			for _, expected := range []string{
				"baseline pod=operator-pod uid=operator-uid", "current pod=operator-pod uid=operator-uid",
				"managerID=containerd://initial-manager", "restarts=2", "ready=true",
				"terminatedID=containerd://terminated-manager reason=Error exitCode=1",
				fmt.Sprintf("previous manager logs available=%t kubeconfigChangeMarker=%t", previousAvailable, previousAvailable),
			} {
				if !strings.Contains(diagnostics, expected) {
					t.Errorf("diagnostics missing %q", expected)
				}
			}
			if strings.Contains(diagnostics, "do-not-print") {
				t.Error("diagnostics exposed non-status content")
			}
			if previousLogReads != 1 {
				t.Errorf("previous log reads = %d, want 1", previousLogReads)
			}
		})
	}
}

// TestOperatorRestartFailureDiagnosticsOSAC7223 checks unwind diagnostics without a Ginkgo suite.
func TestOperatorRestartFailureDiagnosticsOSAC7223(t *testing.T) {
	for _, outcome := range []string{"success", "assertion failure", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			originalFailure := errors.New("original verification failure")
			diagnosticCalls, podReads, historyReads := 0, 0, 0
			kubectl := func(args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "get pod operator-pod -n osac -o json":
					podReads++
					return json.Marshal(operatorRestartTestPod())
				case "logs operator-pod -n osac -c manager --previous --tail=100":
					historyReads++
					return []byte("do-not-print-log-content"), nil
				default:
					t.Fatalf("unexpected diagnostics command: %v", args)
					return nil, nil
				}
			}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				withOperatorRestartDiagnostics(func() {
					switch outcome {
					case "assertion failure":
						assertions := gomega.NewGomega(func(string, ...int) { panic(originalFailure) })
						assertions.Eventually(func() error { return originalFailure },
							10*time.Millisecond, time.Millisecond).Should(gomega.Succeed())
					case "panic":
						panic(originalFailure)
					}
				}, func() {
					diagnosticCalls++
					diagnostics := operatorRestartDiagnostics(operatorRestartTestPod(), kubectl)
					if strings.Contains(diagnostics, "do-not-print") {
						t.Error("failure diagnostics exposed raw log content")
					}
				})
			}()
			wantCalls := 1
			if outcome == "success" {
				wantCalls = 0
				if recovered != nil {
					t.Errorf("successful verification panicked: %v", recovered)
				}
			} else if recovered != originalFailure {
				t.Errorf("verification failure was not preserved: got %v", recovered)
			}
			if diagnosticCalls != wantCalls || podReads != wantCalls || historyReads != wantCalls {
				t.Errorf("diagnostics/pod/history calls = %d/%d/%d, want %d each",
					diagnosticCalls, podReads, historyReads, wantCalls)
			}
		})
	}
}

// operatorRestartTestPod provides a ready baseline manager with explicit process identity.
func operatorRestartTestPod() corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "operator-pod", UID: "operator-uid"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "manager", ContainerID: "containerd://initial-manager", RestartCount: 2, Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}
