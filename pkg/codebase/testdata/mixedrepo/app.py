def greet(name: str) -> str:
    return f"hello, {name}"


def run() -> None:
    print(greet("benchmark"))


if __name__ == "__main__":
    run()
