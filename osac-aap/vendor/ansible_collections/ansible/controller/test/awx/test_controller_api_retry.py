from __future__ import absolute_import, division, print_function

__metaclass__ = type

import io
import json
import sys

import pytest
from unittest import mock

from ansible.module_utils.six.moves.urllib.error import HTTPError
from ansible.module_utils.urls import ConnectionError


def mock_success_response(self, method, url, **kwargs):
    r = mock.MagicMock()
    r.getheader = mock.MagicMock(return_value=None)
    r.read = mock.MagicMock(return_value=b'{}')
    r.status = 200
    return r


def make_http_error(code, body=b''):
    fp = io.BytesIO(body)
    return HTTPError(
        url='https://controller.example.com/api/v2/jobs/',
        code=code,
        msg='Error',
        hdrs={},
        fp=fp,
    )


def create_module(collection_import):
    ControllerAPIModule = collection_import('plugins.module_utils.controller_api').ControllerAPIModule
    cli_data = {'ANSIBLE_MODULE_ARGS': {}}
    testargs = ['module_file2.py', json.dumps(cli_data)]
    with mock.patch.object(sys, 'argv', testargs):
        with mock.patch('ansible.module_utils.urls.Request.open', new=mock_success_response):
            module = ControllerAPIModule(argument_spec=dict())
    module.max_retries = 3
    module.retry_backoff_factor = 2
    return module


# ---------------------------------------------------------------------------
# is_retryable() tests
# ---------------------------------------------------------------------------

@pytest.mark.parametrize(
    'status_code, method, endpoint, expected',
    [
        (502, 'GET', '/api/v2/jobs/', True),
        (502, 'POST', '/api/v2/jobs/', True),
        (502, 'PATCH', '/api/v2/jobs/1/', True),
        (502, 'DELETE', '/api/v2/jobs/1/', True),
        (503, 'GET', '/api/v2/jobs/', True),
        (503, 'POST', '/api/v2/job_templates/', True),
        (503, 'DELETE', '/api/v2/jobs/1/', True),
        (500, 'GET', '/api/v2/jobs/', True),
        (500, 'PATCH', '/api/v2/jobs/1/', True),
        (500, 'DELETE', '/api/v2/jobs/1/', True),
        (504, 'GET', '/api/v2/jobs/', True),
        (504, 'PATCH', '/api/v2/jobs/1/', True),
        (504, 'DELETE', '/api/v2/jobs/1/', True),
        (500, 'POST', '/api/v2/job_templates/', True),
        (500, 'POST', '/api/v2/job_templates/1/launch/', False),
        (500, 'POST', '/api/v2/jobs/1/relaunch/', False),
        (500, 'POST', '/api/v2/job_templates/1/callback/', False),
        (504, 'POST', '/api/v2/job_templates/1/launch/', False),
        (504, 'POST', '/api/v2/workflow_job_templates/1/launch/', False),
        (500, 'POST', '/api/v2/ad_hoc_commands/', False),
        (500, 'POST', '/api/v2/ad_hoc_commands', False),
        (504, 'PATCH', '/api/v2/ad_hoc_commands/1/', True),
        (401, 'GET', '/api/v2/jobs/', False),
        (403, 'POST', '/api/v2/jobs/', False),
        (404, 'GET', '/api/v2/jobs/999/', False),
        (400, 'POST', '/api/v2/jobs/', False),
        (405, 'DELETE', '/api/v2/jobs/1/', False),
    ],
    ids=[
        '502-GET-always-retryable',
        '502-POST-always-retryable',
        '502-PATCH-always-retryable',
        '502-DELETE-always-retryable',
        '503-GET-always-retryable',
        '503-POST-always-retryable',
        '503-DELETE-always-retryable',
        '500-GET-idempotent-retryable',
        '500-PATCH-idempotent-retryable',
        '500-DELETE-idempotent-retryable',
        '504-GET-idempotent-retryable',
        '504-PATCH-idempotent-retryable',
        '504-DELETE-idempotent-retryable',
        '500-POST-safe-endpoint',
        '500-POST-launch-not-retryable',
        '500-POST-relaunch-not-retryable',
        '500-POST-callback-not-retryable',
        '504-POST-launch-not-retryable',
        '504-POST-wfjt-launch-not-retryable',
        '500-POST-ad-hoc-commands-not-retryable',
        '500-POST-ad-hoc-commands-no-trailing-slash',
        '504-PATCH-ad-hoc-commands-retryable',
        '401-not-retryable',
        '403-not-retryable',
        '404-not-retryable',
        '400-not-retryable',
        '405-not-retryable',
    ],
)
def test_is_retryable(collection_import, silence_warning, status_code, method, endpoint, expected):
    module = create_module(collection_import)
    assert module.is_retryable(status_code, method, endpoint) is expected


# ---------------------------------------------------------------------------
# make_request() retry behavior tests
# ---------------------------------------------------------------------------

class AnsibleFailJson(Exception):
    pass


@pytest.fixture
def retry_module(collection_import, silence_warning):
    module = create_module(collection_import)
    module.fail_json = mock.MagicMock(side_effect=AnsibleFailJson)
    module.warn = mock.MagicMock()
    module.version_checked = True
    return module


def test_retry_on_502_then_success(retry_module):
    success = mock.MagicMock()
    success.read.return_value = b'{}'
    success.status = 200

    retry_module.session.open = mock.MagicMock(
        side_effect=[make_http_error(502), success]
    )

    with mock.patch('time.sleep'):
        result = retry_module.make_request('GET', '/api/v2/jobs/')

    assert result['status_code'] == 200
    assert retry_module.session.open.call_count == 2
    retry_module.warn.assert_called()


def test_retry_on_connection_error_then_success(retry_module):
    success = mock.MagicMock()
    success.read.return_value = b'{}'
    success.status = 200

    retry_module.session.open = mock.MagicMock(
        side_effect=[ConnectionError('Connection refused'), success]
    )

    with mock.patch('time.sleep'):
        result = retry_module.make_request('GET', '/api/v2/jobs/')

    assert result['status_code'] == 200
    assert retry_module.session.open.call_count == 2


def test_exhausts_retries_on_503(retry_module):
    retry_module.session.open = mock.MagicMock(
        side_effect=make_http_error(503)
    )

    with mock.patch('time.sleep'):
        with pytest.raises(AnsibleFailJson):
            retry_module.make_request('GET', '/api/v2/jobs/')

    assert retry_module.session.open.call_count == retry_module.max_retries + 1
    fail_msg = retry_module.fail_json.call_args[1]['msg']
    assert 'after {0} retries'.format(retry_module.max_retries) in fail_msg


def test_no_retry_on_401(retry_module):
    retry_module.session.open = mock.MagicMock(
        side_effect=make_http_error(401)
    )

    with mock.patch('time.sleep'):
        with pytest.raises(AnsibleFailJson):
            retry_module.make_request('GET', '/api/v2/jobs/')

    assert retry_module.session.open.call_count == 1
    fail_msg = retry_module.fail_json.call_args[1]['msg']
    assert 'Invalid authentication credentials' in fail_msg


def test_no_retry_on_500_post_launch(retry_module):
    retry_module.session.open = mock.MagicMock(
        side_effect=make_http_error(500)
    )

    with mock.patch('time.sleep'):
        with pytest.raises(AnsibleFailJson):
            retry_module.make_request('POST', '/api/v2/job_templates/1/launch/')

    assert retry_module.session.open.call_count == 1
    fail_msg = retry_module.fail_json.call_args[1]['msg']
    assert 'server error' in fail_msg


def test_retry_on_403_database_unavailable(retry_module):
    body = b'{"detail": "Unable to connect to database"}'
    success = mock.MagicMock()
    success.read.return_value = b'{}'
    success.status = 200

    retry_module.session.open = mock.MagicMock(
        side_effect=[make_http_error(403, body), success]
    )

    with mock.patch('time.sleep'):
        result = retry_module.make_request('GET', '/api/v2/jobs/')

    assert result['status_code'] == 200
    assert retry_module.session.open.call_count == 2


def test_no_retry_on_403_permission_denied(retry_module):
    body = b'{"detail": "You do not have permission to perform this action."}'
    retry_module.session.open = mock.MagicMock(
        side_effect=make_http_error(403, body)
    )

    with mock.patch('time.sleep'):
        with pytest.raises(AnsibleFailJson):
            retry_module.make_request('GET', '/api/v2/jobs/')

    assert retry_module.session.open.call_count == 1
    fail_msg = retry_module.fail_json.call_args[1]['msg']
    assert "don't have permission" in fail_msg


def test_backoff_sleep_called(retry_module):
    retry_module.session.open = mock.MagicMock(
        side_effect=make_http_error(502)
    )

    with mock.patch('time.sleep') as mock_sleep:
        with pytest.raises(AnsibleFailJson):
            retry_module.make_request('GET', '/api/v2/jobs/')

    assert mock_sleep.call_count == retry_module.max_retries


def test_404_returns_none_when_requested(retry_module):
    retry_module.session.open = mock.MagicMock(
        side_effect=make_http_error(404)
    )

    with mock.patch('time.sleep'):
        result = retry_module.make_request('GET', '/api/v2/jobs/999/', return_none_on_404=True)

    assert result is None
    assert retry_module.session.open.call_count == 1
