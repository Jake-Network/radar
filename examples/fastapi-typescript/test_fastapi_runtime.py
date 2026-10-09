"""Opt-in real FastAPI response test; discovery never imports this module."""
import unittest
from fastapi.testclient import TestClient
from backend.app import app

class RuntimeResponse(unittest.TestCase):
    def test_registered_nested_router_matches_frontend_contract(self):
        with TestClient(app) as client:
            response = client.get('/api/v1/users/current')
        self.assertEqual(response.status_code, 200)
        payload = response.json()
        self.assertEqual(payload['profile']['email'], 'reader@example.invalid')
        self.assertIsInstance(payload['id'], int)
        self.assertEqual(payload['display_name'], 'Reader')
