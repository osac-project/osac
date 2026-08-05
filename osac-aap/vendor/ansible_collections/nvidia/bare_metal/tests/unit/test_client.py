# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

import json
import sys
import os
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..', 'plugins'))

from module_utils.client import BareMetalClient


def make_module(api_url='https://api.example.com', api_token='test-token', org='test-org', api_path_prefix=None):
    module = MagicMock()
    module.params = {
        'api_url': api_url,
        'api_token': api_token,
        'org': org,
        'api_path_prefix': api_path_prefix,
    }
    return module


class TestBareMetalClientInit:
    def test_url_stripping(self):
        module = make_module(api_url='https://api.example.com/')
        client = BareMetalClient(module)
        assert client.api_url == 'https://api.example.com'

    def test_org_stored(self):
        module = make_module(org='my-org')
        client = BareMetalClient(module)
        assert client.org == 'my-org'


class TestBareMetalClientUrl:
    def test_url_building(self):
        module = make_module()
        client = BareMetalClient(module)
        url = client._url('/v2/org/{org}/carbide/vpc')
        assert url == 'https://api.example.com/v2/org/test-org/carbide/vpc'

    def test_url_no_org(self):
        module = make_module()
        client = BareMetalClient(module)
        url = client._url('/v2/health')
        assert url == 'https://api.example.com/v2/health'

    def test_url_forge_prefix(self):
        module = make_module(api_path_prefix='forge')
        client = BareMetalClient(module)
        url = client._url('/v2/org/{org}/carbide/vpc')
        assert url == 'https://api.example.com/v2/org/test-org/forge/vpc'

    def test_url_forge_prefix_with_item_path(self):
        module = make_module(api_path_prefix='forge')
        client = BareMetalClient(module)
        url = client._url('/v2/org/{org}/carbide/instance/type/{instanceTypeId}')
        assert url == 'https://api.example.com/v2/org/test-org/forge/instance/type/{instanceTypeId}'

    def test_url_default_prefix_unchanged(self):
        module = make_module(api_path_prefix='carbide')
        client = BareMetalClient(module)
        url = client._url('/v2/org/{org}/carbide/vpc')
        assert url == 'https://api.example.com/v2/org/test-org/carbide/vpc'

    def test_url_none_prefix_defaults_to_carbide(self):
        module = make_module(api_path_prefix=None)
        client = BareMetalClient(module)
        url = client._url('/v2/org/{org}/carbide/vpc')
        assert url == 'https://api.example.com/v2/org/test-org/carbide/vpc'


class TestBareMetalClientHeaders:
    def test_headers(self):
        module = make_module(api_token='my-jwt-token')
        client = BareMetalClient(module)
        headers = client._headers()
        assert headers['Authorization'] == 'Bearer my-jwt-token'
        assert headers['Content-Type'] == 'application/json'
        assert headers['Accept'] == 'application/json'


class TestBareMetalClientGet:
    @patch('module_utils.client.open_url')
    def test_get_success(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        mock_resp = MagicMock()
        mock_resp.getcode.return_value = 200
        mock_resp.read.return_value = json.dumps({'id': '123', 'name': 'test'}).encode()
        mock_open_url.return_value = mock_resp

        result = client.get('/v2/org/{org}/carbide/vpc/123')
        assert result == {'id': '123', 'name': 'test'}

    @patch('module_utils.client.open_url')
    def test_get_404(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        error = Exception('HTTP Error 404')
        error.code = 404
        error.read = MagicMock(return_value=b'Not found')
        mock_open_url.side_effect = error

        result = client.get('/v2/org/{org}/carbide/vpc/nonexistent')
        assert result is None


class TestBareMetalClientCreate:
    @patch('module_utils.client.open_url')
    def test_create(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        created = {'id': '456', 'name': 'new-vpc', 'status': 'Pending'}
        mock_resp = MagicMock()
        mock_resp.getcode.return_value = 201
        mock_resp.read.return_value = json.dumps(created).encode()
        mock_open_url.return_value = mock_resp

        result = client.create('/v2/org/{org}/carbide/vpc', {'name': 'new-vpc'})
        assert result == created

        # Verify the call
        call_args = mock_open_url.call_args
        assert call_args[1]['method'] == 'POST'
        body = json.loads(call_args[1]['data'])
        assert body == {'name': 'new-vpc'}


class TestBareMetalClientUpdate:
    @patch('module_utils.client.open_url')
    def test_update(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        updated = {'id': '123', 'name': 'updated-vpc'}
        mock_resp = MagicMock()
        mock_resp.getcode.return_value = 200
        mock_resp.read.return_value = json.dumps(updated).encode()
        mock_open_url.return_value = mock_resp

        result = client.update('/v2/org/{org}/carbide/vpc/123', {'name': 'updated-vpc'})
        assert result == updated


class TestBareMetalClientDelete:
    @patch('module_utils.client.open_url')
    def test_delete_204(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        mock_resp = MagicMock()
        mock_resp.getcode.return_value = 204
        mock_resp.read.return_value = b''
        mock_open_url.return_value = mock_resp

        result = client.delete('/v2/org/{org}/carbide/vpc/123')
        assert result is None


class TestBareMetalClientListAll:
    @patch('module_utils.client.open_url')
    def test_single_page(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        items = [{'id': '1'}, {'id': '2'}]
        mock_resp = MagicMock()
        mock_resp.getcode.return_value = 200
        mock_resp.read.return_value = json.dumps(items).encode()
        mock_resp.headers = {'X-Pagination': json.dumps({'pageNumber': 1, 'pageSize': 100, 'total': 2})}
        mock_open_url.return_value = mock_resp

        result = client.list_all('/v2/org/{org}/carbide/vpc')
        assert len(result) == 2
        assert result[0]['id'] == '1'

    @patch('module_utils.client.open_url')
    def test_multi_page(self, mock_open_url):
        module = make_module()
        client = BareMetalClient(module)

        page1 = [{'id': str(i)} for i in range(100)]
        page2 = [{'id': str(i)} for i in range(100, 150)]

        resp1 = MagicMock()
        resp1.getcode.return_value = 200
        resp1.read.return_value = json.dumps(page1).encode()
        resp1.headers = {'X-Pagination': json.dumps({'pageNumber': 1, 'pageSize': 100, 'total': 150})}

        resp2 = MagicMock()
        resp2.getcode.return_value = 200
        resp2.read.return_value = json.dumps(page2).encode()
        resp2.headers = {'X-Pagination': json.dumps({'pageNumber': 2, 'pageSize': 100, 'total': 150})}

        mock_open_url.side_effect = [resp1, resp2]

        result = client.list_all('/v2/org/{org}/carbide/vpc')
        assert len(result) == 150
