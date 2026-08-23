"""Worker-side error classification for Google ADK model Activities."""

from google.genai.errors import ClientError
from temporalio.exceptions import ApplicationError
from temporalio.worker import (
    ActivityInboundInterceptor,
    ExecuteActivityInput,
    Interceptor,
)

INVALID_GOOGLE_KEY_MESSAGE = (
    "Gemini rejected GOOGLE_API_KEY. Replace it in the repository-root .env "
    "and restart the worker."
)


def _is_invalid_google_key(error: ClientError) -> bool:
    """Identify only permanent credential failures, not every Google 4xx."""
    message = str(error)
    return error.code in {400, 401, 403} and (
        "API_KEY_INVALID" in message or "API key not valid" in message
    )


class _GoogleAdkActivityInterceptor(ActivityInboundInterceptor):
    async def execute_activity(self, input: ExecuteActivityInput):
        try:
            return await self.next.execute_activity(input)
        except ClientError as error:
            if _is_invalid_google_key(error):
                raise ApplicationError(
                    INVALID_GOOGLE_KEY_MESSAGE,
                    type="LLMFatalError",
                    non_retryable=True,
                ) from error
            raise


class GoogleAdkErrorInterceptor(Interceptor):
    """Make rejected Google credentials fail immediately and readably."""

    def intercept_activity(
        self, next: ActivityInboundInterceptor
    ) -> ActivityInboundInterceptor:
        return _GoogleAdkActivityInterceptor(next)
