"""Public errors contain stable codes, never raw driver or credential details."""


class InspectionError(Exception):
    def __init__(self, code: str, status_code: int = 409) -> None:
        self.code = code
        self.status_code = status_code
        super().__init__(code)
