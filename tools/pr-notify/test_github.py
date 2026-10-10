"""Unit tests for GitHub data collection failure handling."""

import unittest
from unittest.mock import patch

from github import (
    GitHubFetchError,
    _fetch_connection_pages,
    _fetch_repo_prs,
    _new_connection_state,
    _parse_pr_nodes,
    fetch_open_prs,
)


def _make_graphql_pr_node(**overrides) -> dict:
    """Build a minimal GraphQL PR node dict with sensible defaults."""
    defaults = {
        "number": 1,
        "title": "Test PR",
        "body": "",
        "url": "https://github.com/osac-project/osac/pull/1",
        "author": {"login": "alice"},
        "createdAt": "2026-04-20T10:00:00Z",
        "isDraft": False,
        "mergeable": "MERGEABLE",
        "labels": {"pageInfo": {"hasNextPage": False}, "nodes": []},
        "reviews": {"pageInfo": {"hasPreviousPage": False}, "nodes": []},
        "reviewRequests": {"pageInfo": {"hasNextPage": False}, "nodes": []},
        "commits": {
            "nodes": [
                {
                    "commit": {
                        "committedDate": "2026-04-20T10:00:00Z",
                        "statusCheckRollup": None,
                    }
                }
            ]
        },
    }
    defaults.update(overrides)
    return defaults


class TestFetchFailures(unittest.TestCase):
    """A dashboard generation must fail rather than publish partial data."""

    @patch("github._run_graphql_query")
    def test_missing_repository_data_fails(self, mock_query):
        mock_query.return_value = {"data": {"repo_0": None}}

        with self.assertRaises(GitHubFetchError):
            _fetch_repo_prs("osac-project/osac")

    @patch("github._run_graphql_query")
    def test_unavailable_data_fails(self, mock_query):
        mock_query.return_value = {"errors": [{"message": "Repository unavailable"}]}

        with self.assertRaises(GitHubFetchError):
            _fetch_repo_prs("osac-project/osac")

    @patch("github._run_graphql_query")
    def test_open_pr_pagination_without_cursor_fails(self, mock_query):
        mock_query.return_value = {
            "data": {
                "repo_0": {
                    "nameWithOwner": "osac-project/osac",
                    "pullRequests": {
                        "totalCount": 51,
                        "pageInfo": {"hasNextPage": True},
                        "nodes": [],
                    },
                }
            }
        }

        with self.assertRaises(GitHubFetchError):
            _fetch_repo_prs("osac-project/osac")

    @patch("github.time.sleep")
    @patch("github._run_graphql_query")
    def test_open_pr_pagination_is_collected(self, mock_query, mock_sleep):
        mock_query.side_effect = [
            {
                "data": {
                    "repo_0": {
                        "nameWithOwner": "osac-project/osac",
                        "pullRequests": {
                            "pageInfo": {
                                "hasNextPage": True,
                                "endCursor": "cursor-1",
                            },
                            "nodes": [_make_graphql_pr_node(number=1)],
                        },
                    }
                }
            },
            {
                "data": {
                    "repo_0": {
                        "nameWithOwner": "osac-project/osac",
                        "pullRequests": {
                            "pageInfo": {"hasNextPage": False},
                            "nodes": [_make_graphql_pr_node(number=2)],
                        },
                    }
                }
            },
        ]

        prs = _fetch_repo_prs("osac-project/osac")

        self.assertEqual(len(prs), 2)
        self.assertEqual(mock_query.call_count, 2)
        self.assertIn('after: "cursor-1"', mock_query.call_args_list[1].args[0])
        mock_sleep.assert_called_once()

    @patch("github._run_graphql_query")
    def test_check_context_pagination_fails(self, mock_query):
        mock_query.return_value = {
            "data": {
                "repo_0": {
                    "nameWithOwner": "osac-project/osac",
                    "pullRequests": {
                        "totalCount": 1,
                        "pageInfo": {"hasNextPage": False},
                        "nodes": [
                            {
                                "title": "More checks",
                                "body": "",
                                "commits": {
                                    "nodes": [
                                        {
                                            "commit": {
                                                "statusCheckRollup": {
                                                    "contexts": {
                                                        "pageInfo": {"hasNextPage": True},
                                                        "nodes": [],
                                                    }
                                                }
                                            }
                                        }
                                    ]
                                },
                            }
                        ],
                    },
                }
            }
        }

        with self.assertRaises(GitHubFetchError):
            _fetch_repo_prs("osac-project/osac")

    @patch("github._run_graphql_query")
    def test_review_history_pagination_fails(self, mock_query):
        mock_query.return_value = {
            "data": {
                "repo_0": {
                    "nameWithOwner": "osac-project/osac",
                    "pullRequests": {
                        "totalCount": 1,
                        "pageInfo": {"hasNextPage": False},
                        "nodes": [
                            {
                                "title": "More reviews",
                                "body": "",
                                "reviews": {
                                    "pageInfo": {"hasPreviousPage": True},
                                    "nodes": [],
                                },
                            }
                        ],
                    },
                }
            }
        }

        with self.assertRaises(GitHubFetchError):
            _fetch_repo_prs("osac-project/osac")

    @patch("github._run_graphql_query")
    def test_nested_connection_pagination_is_collected(self, mock_query):
        for connection in ("labels", "reviews", "reviewRequests", "contexts"):
            with self.subTest(connection=connection):
                mock_query.reset_mock()
                if connection == "contexts":
                    pull_request = {
                        "commits": {
                            "nodes": [
                                {
                                    "commit": {
                                        "statusCheckRollup": {
                                            "contexts": {
                                                "pageInfo": {
                                                    "hasPreviousPage": False,
                                                    "hasNextPage": False,
                                                },
                                                "nodes": [{"name": "older"}],
                                            }
                                        }
                                    }
                                }
                            ]
                        }
                    }
                else:
                    pull_request = {
                        connection: {
                            "pageInfo": {
                                "hasPreviousPage": False,
                                "hasNextPage": False,
                            },
                            "nodes": [{"name": "older"}],
                        }
                    }
                mock_query.return_value = {
                    "data": {
                        "repository": {
                            f"page_1_{connection}": pull_request
                        }
                    }
                }
                initial = {
                    "pageInfo": (
                        {"hasPreviousPage": True, "startCursor": "cursor"}
                        if connection == "reviews"
                        else {"hasNextPage": True, "endCursor": "cursor"}
                    ),
                    "nodes": [{"name": "current"}],
                }
                state = _new_connection_state(
                    "osac-project/osac", 1, connection, initial
                )
                states = {(1, connection): state}
                _fetch_connection_pages("osac-project/osac", states)

                self.assertEqual(len(state["result"]["nodes"]), 2)

    def test_nested_connection_without_cursor_fails(self):
        for connection in ("labels", "reviews", "reviewRequests", "contexts"):
            with self.subTest(connection=connection):
                initial = {
                    "pageInfo": (
                        {"hasPreviousPage": True}
                        if connection == "reviews"
                        else {"hasNextPage": True}
                    ),
                    "nodes": [],
                }
                with self.assertRaises(GitHubFetchError):
                    state = _new_connection_state(
                        "osac-project/osac", 1, connection, initial
                    )
                    _fetch_connection_pages("osac-project/osac", {(1, connection): state})

    @patch("github._fetch_repo_prs")
    def test_one_repository_failure_stops_collection(self, mock_fetch_repo):
        mock_fetch_repo.side_effect = GitHubFetchError("GitHub data is unavailable")

        with self.assertRaises(GitHubFetchError):
            fetch_open_prs(["osac-project/osac", "osac-project/osac-ui"])


class TestParseBodyField(unittest.TestCase):
    """Verify that the PR body is propagated to PRData."""

    def test_body_populated_from_graphql_node(self):
        """PR body from the GraphQL response is stored in PRData.body."""
        node = _make_graphql_pr_node(body="This is a PR description.")
        results = _parse_pr_nodes("osac-project/osac", [node])
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0].body, "This is a PR description.")

    def test_body_defaults_to_empty_when_missing(self):
        """When the GraphQL node has no body field, PRData.body defaults to ''."""
        node = _make_graphql_pr_node()
        del node["body"]
        results = _parse_pr_nodes("osac-project/osac", [node])
        self.assertEqual(results[0].body, "")

    def test_body_with_attribution_pattern(self):
        """Body containing an attribution pattern is stored verbatim."""
        body_text = "@janboll requested in [Slack](https://slack.com/t/123)"
        node = _make_graphql_pr_node(body=body_text)
        results = _parse_pr_nodes("osac-project/osac", [node])
        self.assertEqual(results[0].body, body_text)


if __name__ == "__main__":
    unittest.main()
