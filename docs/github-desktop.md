# GitHub Desktop and the GitHub Copilot app with local Ollama

"The GitHub desktop app" can mean two different products. Both now support your own model provider ("BYOK"), including Ollama. This guide covers both:

| Product | What it is | AI features you can run locally |
| --- | --- | --- |
| [**GitHub Desktop**](#part-1-github-desktop) | The Git GUI client (`desktop/desktop`) | Copilot commit messages and conflict resolution |
| [**GitHub Copilot app**](#part-2-github-copilot-app) | A newer standalone app for agent-driven development, built on Copilot CLI | Agent sessions and chats |

Both apps **still require you to sign in with a GitHub account**, even when inference is local.

---

## Part 1: GitHub Desktop

### What runs where

| Feature | Local with this setup? |
| --- | --- |
| Copilot commit message generation | ✅ Yes, when you pick your Ollama model for this feature |
| Copilot conflict resolution | ✅ Yes, when you pick your Ollama model for this feature |
| Git operations, GitHub sign-in, and pushing and pulling from github.com | ❌ No. These always use GitHub, which is the point of the app. |
| Usage stats | ✅ You can opt out ([step 4](#step-4-turn-off-usage-stats)) |
| Update checks | ❌ No setting found to turn them off |

### Requirements

- GitHub Desktop, recent enough to have **Settings → Copilot → Providers**.
- Signed in to a GitHub account with access to **Copilot in GitHub Desktop**. Org- or enterprise-managed seats need that feature turned on. GitHub's docs say custom providers "require access to Copilot in GitHub Desktop."
- Ollama configured as described in [Shared setup](../README.md#shared-setup-install-ollama). Commit messages don't need a large context or tool calling, so a small, fast model works well here, for example `ollama pull qwen3.6`.

### Step 1: Add Ollama as a provider

1. Open the settings:
   - **macOS:** **GitHub Desktop → Settings**.
   - **Windows:** **File → Options**.
2. In the sidebar, click **Copilot**, then open the **Providers** tab.
3. Click **Add Provider** and fill in:

   | Field | Value |
   | --- | --- |
   | Name | `Ollama (local)` |
   | Type | **OpenAI / OpenAI-compatible** |
   | Base URL | `http://localhost:11434/v1` |
   | API Format | **Chat completions (default)** |
   | Request Timeout (seconds) | Optional. Raise it, for example to `120`, on slow hardware. |
   | Authentication | **None** |

4. Under **Models**, click **Add Model**:
   - **Display Name:** `Qwen 3.6 (local)`
   - **Model Identifier:** `qwen3.6`, exactly as `ollama list` shows it.
   - **Reasoning Effort:** leave as **Default (provider's choice)**.
5. Click **Add** to save the model, then **Add** again to save the provider.

GitHub Desktop stores provider credentials in your operating system's keychain.

> [!NOTE]
> GitHub Desktop only accepts plain `http://` URLs that point to your own machine (`localhost`, any `127.x.x.x` address, or `::1`). Any other address must use `https://`. This rule comes from `isValidBYOKBaseUrl` in Desktop's source. To use a model on another machine, see [Advanced](#advanced-switch-to-llamacpp-or-a-remote-server).

### Step 2: Pick your local model for each feature

In **Settings → Copilot**, choose your Ollama model in the model picker for **commit message generation** and **conflict resolution**. Each feature has its own model selection. Any feature still set to a GitHub-hosted model keeps sending data to GitHub.

### Step 3: Verify

1. Stage a change in any repository.
2. Click the Copilot (sparkle) button in the commit message box.
3. A message should appear. In a terminal, run `ollama ps` to confirm your model is loaded.

### Step 4: Turn off usage stats

1. Open **Settings** (macOS) or **Options** (Windows), then the **Advanced** tab.
2. Under **Usage**, clear **Help GitHub Desktop improve by submitting usage stats**.

What to know:

- The setting is stored in the app's internal browser storage under the key `stats-opt-out`. There's no config file or command-line flag for it, so you can't easily script it.
- Opting out sends one final ping that records the opt-out itself.
- For a hard guarantee, block these telemetry hosts at your firewall or DNS filter. Both come from `app/src/lib/stats/stats-store.ts`.
  - `cafe.github.com`, the current endpoint.
  - `central.github.com`, the legacy endpoint.
- Desktop's auto-updater checks for new versions on launch. We found no user setting to turn it off. **Unverified:** whether a hidden option exists.
- See [Desktop's published list of usage data it collects](https://github.com/desktop/desktop/blob/development/docs/process/usage-data.md).

Then complete [Shared privacy settings on GitHub.com](../README.md#shared-privacy-settings-on-githubcom).

---

## Part 2: GitHub Copilot app

The [GitHub Copilot app](https://docs.github.com/en/copilot/concepts/copilot-surfaces/github-copilot-app) is a desktop app for macOS, Windows, and Linux that runs parallel agent sessions, each with its own Git worktree. It's built on Copilot CLI. BYOK support is in **public preview** and may change.

### Requirements

- The app, downloaded from <https://github.com/features/ai/github-app>.
- A GitHub account. You must sign in, but **you don't need a Copilot plan** when you use your own provider. Copilot Business and Enterprise users need the **GitHub Copilot app** policy turned on. It's on by default and separate from the Copilot CLI policy.
- Ollama configured as described in [Shared setup](../README.md#shared-setup-install-ollama), with a tool-capable model and a large context. The app runs full coding agents, so follow the context advice in the [Copilot CLI guide](copilot-cli.md#step-1-prepare-ollama).

### Step 1: Add Ollama

- **On first launch:** after you sign in, choose to **continue with your own model provider**, select **Ollama**, enter its details, and click **Save and continue**.
- **Later:** open the app settings, click **Model providers**, then **Add provider**. Select **Ollama**, enter the details, and click **Add provider**.

Use `http://localhost:11434` (or `http://127.0.0.1:11434`) as the Ollama address. The app stores provider credentials in your system's credential store.

### Step 2: Use it

When you start a session or chat, pick your Ollama model in the model picker. Your models appear alongside GitHub-hosted models if you also have a Copilot plan. Pick the local model for **every** session that should stay local.

### Privacy notes for the app

- **Unverified:** a telemetry opt-out inside the app. The docs don't describe one. Look for a usage-data or telemetry option in the app settings.
- Sessions can run in a **cloud sandbox** or locally. Choose **local** to keep code on your machine.
- GitHub features in the app, such as issues, PRs, CI status, and search, contact GitHub by design.

---

## Advanced: Switch to llama.cpp, or a remote server

### Start `llama-server`

**Local server:**

```shell
llama-server -hf ggml-org/gpt-oss-20b-GGUF --jinja -c 65536 --host 127.0.0.1 --port 8080 --alias gpt-oss-20b
```

**Remote server:** listen on the network and require a key.

```shell
llama-server -hf ggml-org/gpt-oss-20b-GGUF --jinja -c 65536 --host 0.0.0.0 --port 8080 --alias gpt-oss-20b --api-key YOUR_SECRET
```

Always pass `--port`, because the default port in current builds is `9931`. The `--alias` value is the model identifier that clients must use.

### GitHub Desktop

| Server location | Base URL | Authentication |
| --- | --- | --- |
| Same machine | `http://127.0.0.1:8080/v1` | **None**, or **API key** if you used `--api-key` |
| Remote, through an SSH tunnel | `http://127.0.0.1:8080/v1` after running `ssh -N -L 8080:127.0.0.1:8080 user@server` | Same as above |
| Remote, behind an HTTPS reverse proxy | `https://llm.example.com/v1` | **API key** set to `YOUR_SECRET` |
| Remote by plain IP, such as `http://192.168.1.50:8080/v1` | ❌ Rejected. Desktop requires HTTPS for non-local addresses. | Use a tunnel or HTTPS instead. |

For all options:

- **Type:** OpenAI / OpenAI-compatible.
- **API Format:** Chat completions.
- **Model Identifier:** the `--alias` value, for example `gpt-oss-20b`.

The same rules apply to a remote Ollama server, with port 11434 instead of 8080.

Quick connection test, which works for both servers. Leave out the `Authorization` header if you didn't set `--api-key`:

```shell
curl -s https://llm.example.com/v1/chat/completions -H "Authorization: Bearer YOUR_SECRET" -H "Content-Type: application/json" -d '{"model":"gpt-oss-20b","messages":[{"role":"user","content":"Write a one-line commit message for: fix typo in README"}]}'
```

### GitHub Copilot app

Add a provider with the type **Any OpenAI-compatible HTTP endpoint**:

- **Base URL:** `http://127.0.0.1:8080/v1`, or your remote `https://` URL.
- **API key:** the key you set with `--api-key`.
- **Model:** the `--alias` value.

For a remote Ollama server, select **Ollama** and enter the remote address. **Unverified:** whether the app accepts plain `http://` for non-local hosts. If it rejects them, use an SSH tunnel or HTTPS, as with GitHub Desktop.

### Security for remote servers

Neither Ollama nor `llama-server` encrypts traffic, and Ollama has no built-in authentication. Your diffs and code are in every prompt. Choose one of these options:

- **SSH tunnel (simplest):** works with Desktop's `localhost` rule.
- **Private network:** use a VPN or mesh network such as WireGuard or Tailscale, plus firewall rules.
- **Reverse proxy with TLS,** such as Caddy or nginx, plus `--api-key` or proxy authentication. This is required for Desktop if you don't use a tunnel.

Never expose port 11434 or 8080 directly to the internet.

## Sources

- [Configuring Copilot in GitHub Desktop (BYOK)](https://docs.github.com/en/desktop/configuring-and-customizing-github-desktop/configuring-copilot-in-github-desktop)
- GitHub Desktop source: [`app/src/lib/copilot/byok.ts`](https://github.com/desktop/desktop/blob/development/app/src/lib/copilot/byok.ts) (URL rules), [`app/src/lib/stats/stats-store.ts`](https://github.com/desktop/desktop/blob/development/app/src/lib/stats/stats-store.ts) (telemetry), [`app/src/ui/preferences/advanced.tsx`](https://github.com/desktop/desktop/blob/development/app/src/ui/preferences/advanced.tsx) (opt-out UI)
- [GitHub Copilot app overview](https://docs.github.com/en/copilot/concepts/copilot-surfaces/github-copilot-app), [quickstart](https://docs.github.com/en/copilot/get-started/quickstart-copilot-app), and [Adding LLM models to the GitHub Copilot app](https://docs.github.com/en/copilot/how-tos/github-copilot-app/use-byok-models)
- [llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
