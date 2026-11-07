# ollama

A small client for a local Ollama model (`Service.Summarize`). It is not wired into anything yet: it is the starting point for an optional model-backed implementation of the `pkg/attention` interfaces (Classifier, Summarizer, Explainer), which must always go through `attention.Guarded` so a critical event never reaches it and a model can never escalate.

It was `pkg/learning` until milestone 26, when that name went to the learning engine.
