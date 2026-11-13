"""Unit contracts for the capture-only AAP fixture."""

import json
import threading
import unittest
import urllib.error
import urllib.request

from compute_storage_aap import RecorderServer


class RecorderTest(unittest.TestCase):
    def setUp(self):
        self.server = RecorderServer(("127.0.0.1", 0))
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.close)
        self.url = f"http://127.0.0.1:{self.server.server_port}"

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)

    def request(self, path, data=None, method=None):
        body = None if data is None else json.dumps(data).encode()
        request = urllib.request.Request(
            self.url + path, body, headers={"Content-Type": "application/json"}, method=method
        )
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.load(response)

    def test_launch_preserves_disk_ids_and_classes_until_explicit_completion(self):
        payload = {
            "extra_vars": {
                "osac_job_vars": {
                    "resource": {"spec": {"diskVolumeIDs": {"boot": "volume-1"}}},
                    "tenant_storage_classes": [{"tier": "gold", "name": "gold-sc"}],
                }
            }
        }
        result = self.request("/api/v2/job_templates/1/launch/", payload)
        job_id = result["id"]
        job = self.request(f"/api/v2/jobs/{job_id}/")
        self.assertEqual(job["status"], "running")
        self.assertEqual(json.loads(job["extra_vars"]), payload["extra_vars"])
        captures = self.request("/captures")
        self.assertEqual(captures["jobs"][0]["template"], "osac-create-compute-instance")
        self.assertEqual(captures["jobs"][0]["payload"], payload)
        self.request(f"/complete/{job_id}", {"status": "successful"})
        self.assertEqual(self.request(f"/api/v2/jobs/{job_id}/")["status"], "successful")

    def test_only_compute_create_and_delete_templates_exist(self):
        for name, template_id in [
            ("osac-create-compute-instance", 1),
            ("osac-delete-compute-instance", 2),
        ]:
            found = self.request(f"/api/v2/job_templates/?name={name}")
            self.assertEqual(found["results"], [{"id": template_id, "name": name}])
        found = self.request("/api/v2/job_templates/?name=osac-create-storage")
        self.assertEqual(found["results"], [])

    def test_unexpected_launch_fails_and_is_observable(self):
        with self.assertRaises(urllib.error.HTTPError) as error:
            self.request("/api/v2/job_templates/99/launch/", {"extra_vars": {}})
        self.assertEqual(error.exception.code, 404)
        error.exception.close()
        captures = self.request("/captures")
        self.assertEqual(captures["jobs"], [])
        self.assertEqual(captures["unexpected"], ["POST /api/v2/job_templates/99/launch/"])

    def test_cancel_is_terminal_without_executing_anything(self):
        job_id = self.request("/api/v2/job_templates/1/launch/", {"extra_vars": {}})["id"]
        self.request(f"/api/v2/jobs/{job_id}/cancel/", method="POST")
        self.assertEqual(self.request(f"/api/v2/jobs/{job_id}/")["status"], "canceled")

    def test_named_launch_keeps_attempt_label_for_job_recovery(self):
        label = self.request("/api/v2/labels/", {"name": "attempt-1", "organization": 1})
        found = self.request("/api/v2/labels/?name=attempt-1&organization=1")
        self.assertEqual(found["results"], [label])
        job_id = self.request(
            "/api/v2/job_templates/osac-create-compute-instance/launch/",
            {"extra_vars": {}, "labels": [label["id"]]},
        )["id"]
        jobs = self.request("/api/v2/jobs/?labels__name=attempt-1")
        self.assertEqual([job["id"] for job in jobs["results"]], [job_id])
        self.assertEqual(self.request("/api/v2/jobs/?labels__name=other")["results"], [])

    def test_reset_isolates_scenarios(self):
        self.request("/api/v2/job_templates/1/launch/", {"extra_vars": {}})
        self.request("/reset", {})
        self.assertEqual(self.request("/captures"), {"jobs": [], "unexpected": []})

    def test_malformed_launch_does_not_become_a_job(self):
        with self.assertRaises(urllib.error.HTTPError) as error:
            self.request("/api/v2/job_templates/1/launch/", {"extra_vars": "not-an-object"})
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        self.assertEqual(self.request("/captures")["jobs"], [])


if __name__ == "__main__":
    unittest.main()
