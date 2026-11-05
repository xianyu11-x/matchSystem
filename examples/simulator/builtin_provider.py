"""Python reference implementation of the simulator's built-in Providers.

The simulator starts this file with the production JSON-lines runner.  The
runner stores the one-time handshake ``Input`` in ``_provider_context``.  That
context contains the current Contract subset and static Tick values, so this
script can keep its capability directory fixed while returning only Facts
declared by the bound rule.

Supported Contract Facts are the same canonical names as the current Go
defaults: ``waitingCount``, ``preferredRoles``, ``latencyMs``, ``waitingTime``
and ``memberCount``.  ``FactNameWaitingCount`` and ``FactNameWaitingTime`` in
Go are constant aliases, not additional wire names; historical
``queueDepth``/``waiting-count`` aliases are not synthesized by the current Go
Provider and are intentionally not added here.

No third-party package is required.  Keep stdout reserved for the runner's
JSON-lines protocol; diagnostics may be written to stderr.
"""

MAX_INT64 = (1 << 63) - 1

WAITING_COUNT = "waitingCount"
PREFERRED_ROLES = "preferredRoles"
LATENCY_MS = "latencyMs"
WAITING_TIME = "waitingTime"
MEMBER_COUNT = "memberCount"

_SUPPORTED = {
    WAITING_COUNT: ("int64", "tick"),
    PREFERRED_ROLES: ("strings", "object"),
    LATENCY_MS: ("int64", "object"),
    WAITING_TIME: ("int64", "object"),
    MEMBER_COUNT: ("int64", "match"),
}


def describe():
    """Return the fixed capabilities owned by this implementation."""

    return {
        "tick": {
            "id": "simulator.tick-facts",
            "version": "v1",
            "facts": [
                {"name": WAITING_COUNT, "type": "int64", "scope": "tick"},
            ],
        },
        "object": {
            "id": "simulator.object-facts",
            "version": "v1",
            "facts": [
                {
                    "name": PREFERRED_ROLES,
                    "type": "strings",
                    "scope": "object",
                    "maxValues": 3,
                },
                {"name": LATENCY_MS, "type": "int64", "scope": "object"},
                {"name": WAITING_TIME, "type": "int64", "scope": "object"},
            ],
        },
        "match": {
            "id": "simulator.match-facts",
            "version": "v1",
            "facts": [
                {"name": MEMBER_COUNT, "type": "int64", "scope": "match"},
            ],
        },
    }


def _facts():
    """Create the PascalCase Facts wire shape with independent maps."""

    return {"StringLists": {}, "Uint64Lists": {}, "Int64Values": {}}


def _context():
    value = globals().get("_provider_context")
    return value if isinstance(value, dict) else {}


def _declared(name):
    """Return whether a supported Fact is in this rule's Contract."""

    expected = _SUPPORTED.get(name)
    if expected is None:
        return False
    for spec in _context().get("ContractFacts", []):
        if not isinstance(spec, dict):
            continue
        if (
            spec.get("Name") == name
            and spec.get("Type") == expected[0]
            and spec.get("Scope") == expected[1]
        ):
            return True
    return False


def _copy_static_tick():
    result = _facts()
    static = _context().get("TickFacts")
    if not isinstance(static, dict):
        return result
    for wire_name, output_name, type_name in (
        ("StringLists", "StringLists", "strings"),
        ("Uint64Lists", "Uint64Lists", "uint64s"),
        ("Int64Values", "Int64Values", "int64"),
    ):
        values = static.get(wire_name)
        if not isinstance(values, dict):
            continue
        for name, value in values.items():
            expected = _SUPPORTED.get(name)
            if expected is None or expected != (type_name, "tick") or not _declared(name):
                continue
            if type_name == "strings" or type_name == "uint64s":
                result[output_name][name] = list(value)
            else:
                result[output_name][name] = value
    return result


def _waiting_count(data):
    node = data.get("Node") if isinstance(data, dict) else None
    value = node.get("WaitingCount", 0) if isinstance(node, dict) else 0
    return max(0, int(value))


def _elapsed(now, created_at):
    if now <= created_at:
        return 0
    elapsed = now - created_at
    return min(elapsed, MAX_INT64)


def _object_facts(data):
    result = _facts()
    observations = data.get("ObjectFacts") if isinstance(data, dict) else None
    if isinstance(observations, dict):
        strings = observations.get("StringLists")
        if _declared(PREFERRED_ROLES) and isinstance(strings, dict):
            values = strings.get(PREFERRED_ROLES)
            if values is not None:
                result["StringLists"][PREFERRED_ROLES] = list(values)
        integers = observations.get("Int64Values")
        if _declared(LATENCY_MS) and isinstance(integers, dict):
            if LATENCY_MS in integers:
                result["Int64Values"][LATENCY_MS] = integers[LATENCY_MS]

    if _declared(WAITING_TIME):
        ticket = data.get("Ticket") if isinstance(data, dict) else None
        if not isinstance(ticket, dict):
            raise ValueError("object Ticket is required")
        result["Int64Values"][WAITING_TIME] = _elapsed(
            int(data.get("Now", 0)), int(ticket.get("CreatedAt", 0))
        )
    return result


def _initialize_match():
    result = _facts()
    if _declared(MEMBER_COUNT):
        result["Int64Values"][MEMBER_COUNT] = 1
    return result


def _join_match(data):
    result = _facts()
    if not _declared(MEMBER_COUNT):
        return result
    before = data.get("MatchFactsBefore") if isinstance(data, dict) else None
    integers = before.get("Int64Values") if isinstance(before, dict) else None
    current = integers.get(MEMBER_COUNT) if isinstance(integers, dict) else None
    if current is None:
        current = 1
    current = int(current)
    if current >= MAX_INT64:
        raise OverflowError("simulator Match Fact memberCount overflow")
    result["Int64Values"][MEMBER_COUNT] = current + 1
    return result


def provide(method, data):
    """Implement the Tick/Object/Match Provider RPC methods."""

    if method == "tick":
        result = _copy_static_tick()
        if _declared(WAITING_COUNT):
            result["Int64Values"][WAITING_COUNT] = _waiting_count(data)
        return result
    if method == "object":
        return _object_facts(data)
    if method == "initialize":
        return _initialize_match()
    if method == "join":
        return _join_match(data)
    raise ValueError("unknown method: %s" % method)
