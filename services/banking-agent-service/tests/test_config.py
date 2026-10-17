import pytest

from banking_agent.config import Settings


def test_configuration_has_no_secret_in_repr():
    s = Settings(
        "postgresql://app:hidden-pg@localhost/db",
        "redis://:hidden-cache@localhost/0",
        "https://banking.invalid",
    )
    assert "hidden-" not in repr(s)


@pytest.mark.parametrize(
    "origin",
    [
        "file:///tmp/core",
        "https://user:secret@banking.invalid",
        "http://banking.invalid/api",
        "https://banking.invalid/?redirect=evil",
    ],
)
def test_origin_is_explicit_without_secret_or_redirect(origin):
    with pytest.raises(ValueError):
        Settings("postgresql://app:password@localhost/db", "redis://:password@localhost/0", origin)
