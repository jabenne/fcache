# fcache

A simple, thread-safe, file-backed in-memory cache for Go.

Every in-memory entry is backed by a corresponding `.fc` file on disk. Built entirely on the Go standard library — no external dependencies.
