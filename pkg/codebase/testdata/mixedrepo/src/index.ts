export function greet(name: string): string {
  return `hello, ${name}`;
}

export function run(): void {
  console.log(greet("benchmark"));
}
