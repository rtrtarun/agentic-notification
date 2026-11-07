# Milestone 9: Learning Layer with Ollama

## Architecture

- Keep the learning layer lightweight and optional.
- Use a small adapter that can call a local Ollama server for summarizing feedback into recommendations.
- Avoid introducing a full ML pipeline in this milestone.

## Tasks

- Add a learning service package.
- Implement an Ollama-backed summarizer.
- Connect the server to the learning service for future feedback-driven recommendations.

## Implementation

- The learning service posts feedback text to a local Ollama endpoint using the llama3.2 model.
- The service returns a concise recommendation string that can be surfaced later in the product.

## Tests

- Verify the learning service parses a successful Ollama response.

## Review checklist

- [x] Learning adapter exists.
- [x] Ollama integration is available through a simple abstraction.
- [x] Backend tests pass.
