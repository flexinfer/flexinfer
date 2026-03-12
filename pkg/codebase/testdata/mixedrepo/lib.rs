pub fn greet(name: &str) -> String {
    format!("hello, {}", name)
}

pub fn run() {
    println!("{}", greet("benchmark"));
}
