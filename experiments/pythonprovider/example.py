"""Edit multiplier, then explicitly replace the worker between matching rounds."""
multiplier = 1
calls = 0

def provide(method, data):
    global calls
    calls += 1
    if method == "tick":
        return {"Int64Values": {"clock": data["Now"] * multiplier}}
    if method == "object":
        return {"Uint64Lists": {"ids": [data["Ticket"]["TicketID"]]},
                "StringLists": {"labels": ["中文"]}}
    if method == "initialize":
        return {"Int64Values": {"members": 1}}
    if method == "join":
        return {"Int64Values": {"members": data["MatchFactsBefore"]["Int64Values"]["members"] + 1}}
    raise ValueError("unknown method")
