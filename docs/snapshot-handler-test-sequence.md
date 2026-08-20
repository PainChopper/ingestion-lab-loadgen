# `TestSnapshotHandlerReturnsOwnerSnapshot` sequence

This test verifies the complete snapshot request path: an HTTP request reaches `snapshotHandler`, the handler asks the state owner for a snapshot through the command channel, and the returned snapshot becomes the HTTP response.

```mermaid
sequenceDiagram
    participant Test as Test
    participant Handler as snapshotHandler
    participant Owner as fakeStateOwner
    participant Recorder as ResponseRecorder

    Test->>Test: Create expected snapshot
    Test->>Owner: Start goroutine
    Note over Owner: Wait for a command

    Test->>Handler: ServeHTTP(rec, req)
    Handler->>Owner: Send getSnapshot through commands
    Note over Handler,Owner: Unbuffered channel rendezvous

    Owner->>Owner: Verify cmd.kind
    Owner-->>Handler: Send expected through snapshotReply

    Handler->>Recorder: Write HTTP 200
    Handler->>Recorder: Write application/json header
    Handler->>Recorder: Encode snapshot as JSON
    Handler-->>Test: Return from ServeHTTP

    Test->>Recorder: Read status, header, and body
    Test->>Test: Decode body into actual
    Test->>Test: Compare actual with expected
```

## Participants

- `req` is the synthetic `GET /api/loadgen/snapshot` request.
- `rec` records the HTTP response produced by the handler.
- `fakeStateOwner` replaces the real state-owning `main` select loop for this test.
- `expected` is the snapshot returned by the fake state owner; it is not prebuilt JSON.
- `actual` is decoded from the handler's HTTP response body.

The goroutine is required because `snapshotHandler` sends a command and waits for `snapshotReply`. In production, the `main` select loop receives and answers that command. In this test, `fakeStateOwner` performs the same role.

