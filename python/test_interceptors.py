import unittest

from google.genai.errors import ClientError
from temporalio.exceptions import ApplicationError

from interceptors import _GoogleAdkActivityInterceptor


class _FailingActivity:
    def __init__(self, error: Exception) -> None:
        self.error = error

    async def execute_activity(self, _input):
        raise self.error


class GoogleAdkErrorInterceptorTests(unittest.IsolatedAsyncioTestCase):
    async def test_invalid_key_is_a_non_retryable_llm_failure(self) -> None:
        client_error = ClientError(
            400,
            {
                "error": {
                    "code": 400,
                    "message": "API key not valid. Please pass a valid API key.",
                    "status": "INVALID_ARGUMENT",
                    "details": [{"reason": "API_KEY_INVALID"}],
                }
            },
        )
        interceptor = _GoogleAdkActivityInterceptor(_FailingActivity(client_error))

        with self.assertRaises(ApplicationError) as raised:
            await interceptor.execute_activity(None)

        self.assertEqual("LLMFatalError", raised.exception.type)
        self.assertTrue(raised.exception.non_retryable)
        self.assertIn("Gemini rejected GOOGLE_API_KEY", str(raised.exception))

    async def test_rate_limit_remains_retryable_by_temporal(self) -> None:
        client_error = ClientError(
            429,
            {
                "error": {
                    "code": 429,
                    "message": "Resource exhausted.",
                    "status": "RESOURCE_EXHAUSTED",
                }
            },
        )
        interceptor = _GoogleAdkActivityInterceptor(_FailingActivity(client_error))

        with self.assertRaises(ClientError):
            await interceptor.execute_activity(None)


if __name__ == "__main__":
    unittest.main()
