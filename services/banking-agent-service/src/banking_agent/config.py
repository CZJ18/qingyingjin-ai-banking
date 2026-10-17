import os
from dataclasses import dataclass, field
from urllib.parse import urlsplit


@dataclass(frozen=True)
class Settings:
    database_url: str = field(repr=False)
    redis_url: str = field(repr=False)
    banking_url: str
    banking_timeout: float = 5.0
    profile_timezone: str = "Asia/Shanghai"
    agent_service_key: str = field(default="", repr=False)

    def __post_init__(self):
        pg = urlsplit(self.database_url)
        redis = urlsplit(self.redis_url)
        bank = urlsplit(self.banking_url)
        if pg.scheme not in ("postgres", "postgresql") or not pg.hostname or not pg.password:
            raise ValueError("explicit PostgreSQL runtime connection required")
        if redis.scheme not in ("redis", "rediss") or not redis.hostname or not redis.password:
            raise ValueError("explicit protected Redis connection required")
        if (
            bank.scheme not in ("http", "https")
            or not bank.hostname
            or bank.username
            or bank.password
            or bank.query
            or bank.fragment
            or bank.path not in ("", "/")
        ):
            raise ValueError("explicit Banking origin without credentials required")
        if not 0.1 <= self.banking_timeout <= 30:
            raise ValueError("Banking timeout must be between 0.1 and 30 seconds")
        if self.agent_service_key and (
            len(self.agent_service_key) < 32
            or not self.agent_service_key.isascii()
            or "CHANGE_ME" in self.agent_service_key
        ):
            raise ValueError("a separate non-template Agent service key is required")

    @classmethod
    def from_env(cls):
        return cls(
            database_url=os.environ["BANKING_AGENT_DATABASE_URL"],
            redis_url=os.environ["BANKING_AGENT_REDIS_URL"],
            banking_url=os.environ["BANKING_AGENT_BANKING_URL"],
            banking_timeout=float(os.environ.get("BANKING_AGENT_BANKING_TIMEOUT", "5")),
            agent_service_key=os.environ.get("BANKING_AGENT_SERVICE_KEY", ""),
        )
