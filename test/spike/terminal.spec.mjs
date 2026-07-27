import assert from "node:assert/strict";
import { once } from "node:events";
import { spawn } from "node:child_process";
import { test } from "@playwright/test";

let server;
let origin;

test.beforeAll(async () => {
  server = spawn(process.env.HOPTY_SPIKE_BIN ?? "/usr/local/bin/hopty-spike", ["--listen", "127.0.0.1:0"], {
    stdio: ["ignore", "pipe", "pipe"]
  });
  const [line] = await once(server.stdout, "data");
  origin = line.toString("utf8").trim();
  assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/);
});

test.afterAll(async () => {
  if (server?.exitCode === null) {
    server.kill("SIGTERM");
    await once(server, "exit");
  }
});

test("Chromium exchanges PTY frames over a direct Pion DataChannel", async ({ page }) => {
  await page.goto(origin);

  const result = await page.evaluate(async () => {
    const waitFor = async (predicate, timeout = 10_000, message = "timed out") => {
      const deadline = performance.now() + timeout;
      while (!predicate()) {
        if (performance.now() >= deadline) throw new Error(message);
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
    };
    const within = (promise, timeout, message) => Promise.race([
      promise,
      new Promise((_, reject) => setTimeout(() => reject(new Error(message)), timeout))
    ]);
    const waitForGathering = (pc) => new Promise((resolve) => {
      const done = () => {
        pc.removeEventListener("icegatheringstatechange", onStateChange);
        resolve();
      };
      const onStateChange = () => {
        if (pc.iceGatheringState === "complete") done();
      };
      pc.addEventListener("icegatheringstatechange", onStateChange);
      if (pc.iceGatheringState === "complete") done();
    });
    const encoder = new TextEncoder();
    const decoder = new TextDecoder();
    const pc = new RTCPeerConnection();
    const channel = pc.createDataChannel("hopty.terminal.v1", { ordered: true });
    channel.binaryType = "arraybuffer";
    let output = "";
    let sawExit = false;
    const channelOpened = new Promise((resolve, reject) => {
      channel.addEventListener("open", resolve, { once: true });
      channel.addEventListener("error", () => reject(new Error("data channel error")), { once: true });
    });
    const channelClosed = new Promise((resolve) => channel.addEventListener("close", resolve, { once: true }));

    channel.addEventListener("message", (event) => {
      const frame = new Uint8Array(event.data);
      if (frame[0] === 1) output += decoder.decode(frame.slice(1), { stream: true });
      if (frame[0] === 3) sawExit = true;
    });

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    await within(waitForGathering(pc), 10_000, "ICE gathering did not complete");
    const response = await fetch("/offer", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ sdp: pc.localDescription.sdp })
    });
    if (!response.ok) throw new Error(`offer rejected: ${response.status}`);
    const answer = await response.json();
    await pc.setRemoteDescription({ type: "answer", sdp: answer.sdp });
    await within(channelOpened, 10_000, "data channel did not open");
    await new Promise((resolve) => setTimeout(resolve, 100));

    pc.restartIce();
    const restartOffer = await pc.createOffer({ iceRestart: true });
    await pc.setLocalDescription(restartOffer);
    await within(waitForGathering(pc), 10_000, "ICE restart gathering did not complete");
    const restartResponse = await fetch(`/sessions/${answer.id}/offer`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ sdp: pc.localDescription.sdp })
    });
    if (!restartResponse.ok) throw new Error(`ICE restart rejected: ${restartResponse.status}`);
    const restartAnswer = await restartResponse.json();
    await pc.setRemoteDescription({ type: "answer", sdp: restartAnswer.sdp });
    await waitFor(() => pc.connectionState === "connected", 10_000, "ICE restart did not reconnect");

    channel.send(new Uint8Array([2, 0, 100, 0, 40]));
    const input = encoder.encode("stty size; printf 'HOPTY_SPIKE\\n'\n");
    channel.send(new Uint8Array([1, ...input]));
    await waitFor(
      () => output.includes("40 100") && output.includes("HOPTY_SPIKE"),
      10_000,
      `PTY output was incomplete: ${JSON.stringify(output)}`
    );

    channel.send(new Uint8Array([1, 4]));
    await within(channelClosed, 10_000, "Ctrl+D did not close the terminal");
    pc.close();
    return { output, sawExit };
  });

  assert.match(result.output, /40 100/);
  assert.match(result.output, /HOPTY_SPIKE/);
  assert.equal(result.sawExit, true);
});

test("forced disconnection closes the PTY after the ten-second recovery grace", async ({ page }) => {
  await page.goto(origin);

  const elapsed = await page.evaluate(async () => {
    const within = (promise, timeout, message) => Promise.race([
      promise,
      new Promise((_, reject) => setTimeout(() => reject(new Error(message)), timeout))
    ]);
    const waitForGathering = (pc) => new Promise((resolve) => {
      const done = () => {
        pc.removeEventListener("icegatheringstatechange", onStateChange);
        resolve();
      };
      const onStateChange = () => {
        if (pc.iceGatheringState === "complete") done();
      };
      pc.addEventListener("icegatheringstatechange", onStateChange);
      if (pc.iceGatheringState === "complete") done();
    });
    const pc = new RTCPeerConnection();
    const channel = pc.createDataChannel("hopty.terminal.v1", { ordered: true });
    const opened = new Promise((resolve) => channel.addEventListener("open", resolve, { once: true }));
    const closed = new Promise((resolve) => channel.addEventListener("close", resolve, { once: true }));
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    await within(waitForGathering(pc), 10_000, "ICE gathering did not complete");
    const response = await fetch("/offer", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ sdp: pc.localDescription.sdp })
    });
    const answer = await response.json();
    await pc.setRemoteDescription({ type: "answer", sdp: answer.sdp });
    await within(opened, 10_000, "data channel did not open");
    await new Promise((resolve) => setTimeout(resolve, 100));
    const started = performance.now();
    const force = await fetch(`/sessions/${answer.id}/force-disconnect`, { method: "POST" });
    if (!force.ok) throw new Error(`force disconnect rejected: ${force.status}`);
    await within(closed, 12_000, "recovery deadline did not close the channel");
    pc.close();
    return performance.now() - started;
  });

  assert.ok(elapsed >= 10_000, `terminal closed too early: ${elapsed}ms`);
  assert.ok(elapsed <= 11_000, `terminal closed too late: ${elapsed}ms`);
});
