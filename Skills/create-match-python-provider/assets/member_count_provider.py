"""仅支持模拟器标准 memberCount 的 Python Fact Provider。"""

MAX_INT64 = (1 << 63) - 1


def describe():
    return {
        "match": {
            "id": "simulator.match-facts",
            "version": "v1",
            "facts": [{"name": "memberCount", "type": "int64", "scope": "match"}],
        }
    }


def provide(method, data):
    if method in ("tick", "object"):
        return {}
    if method == "initialize":
        return {"Int64Values": {"memberCount": 1}}
    if method == "join":
        current = data["MatchFactsBefore"]["Int64Values"]["memberCount"]
        if current >= MAX_INT64:
            raise OverflowError("memberCount 超出 int64 上限")
        return {"Int64Values": {"memberCount": current + 1}}
    raise ValueError("未知的 Provider 方法: " + method)
