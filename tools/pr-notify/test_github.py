"""Unit tests for GitHub data collection failure handling."""

import unittest
from unittest.mock import patch

from github import GitHubFetchError, _fetch_repo_prs, fetch_open_prs


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
    def test_open_pr_pagination_fails(self, mock_query):
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

    @patch("github._fetch_repo_prs")
    def test_one_repository_failure_stops_collection(self, mock_fetch_repo):
        mock_fetch_repo.side_effect = GitHubFetchError("GitHub data is unavailable")

        with self.assertRaises(GitHubFetchError):
            fetch_open_prs(["osac-project/osac", "osac-project/osac-ui"])


if __name__ == "__main__":
    unittest.main()
