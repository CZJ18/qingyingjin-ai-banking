class DomainError(Exception):
    """Public, sanitized domain failure; never include secrets or SQL errors."""

    def __init__(self, code: str, message: str, status: int):
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status
