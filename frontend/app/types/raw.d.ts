// Allow importing markdown (and other) files as raw strings via Vite's ?raw.
declare module '*.md?raw' {
  const content: string
  export default content
}
