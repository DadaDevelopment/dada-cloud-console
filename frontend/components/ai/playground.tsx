"use client";
import { useState, FormEvent, ChangeEvent } from "react";
import { callInference } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import type { AIModelType } from "@/lib/types";

const TABULAR_TYPES: AIModelType[] = ["sklearn", "xgboost", "lightgbm"];
const FILE_HINTS: Record<string, { accept: string; hint: string }> = {
  huggingface: { accept: "image/*,audio/*", hint: "Optional — text in the prompt is enough for most models" },
  custom: { accept: "*/*", hint: "Anything your container accepts" },
  pytorch: { accept: "image/*", hint: "Optional — vision models" },
  tensorflow: { accept: "image/*", hint: "Optional — vision models" },
};

type Props = {
  projectId: string;
  envId: string;
  name: string;
  modelType: AIModelType;
  ready: boolean;
};

const sampleFor = (t: AIModelType): string => {
  if (TABULAR_TYPES.includes(t)) {
    return JSON.stringify({ instances: [[5.1, 3.5, 1.4, 0.2]] }, null, 2);
  }
  if (t === "huggingface") {
    return JSON.stringify({ inputs: "Hello, world!" }, null, 2);
  }
  if (t === "triton" || t === "pytorch" || t === "tensorflow") {
    return JSON.stringify({
      inputs: [{ name: "input_0", shape: [1, 3], datatype: "FP32", data: [0.1, 0.2, 0.3] }],
    }, null, 2);
  }
  return JSON.stringify({ inputs: [] }, null, 2);
};

export function Playground({ projectId, envId, name, modelType, ready }: Props) {
  const [chatPrompt, setChatPrompt] = useState("");
  const [jsonBody, setJsonBody] = useState(sampleFor(modelType));
  const [file, setFile] = useState<File | null>(null);
  const [loading, setLoading] = useState(false);
  const [response, setResponse] = useState<string | null>(null);
  const [responseStatus, setResponseStatus] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rawJson, setRawJson] = useState(false);

  const isChat = modelType === "huggingface";
  const fileHint = FILE_HINTS[modelType];
  const supportsFile = !!fileHint;

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!ready) return;
    setLoading(true);
    setError(null);
    setResponse(null);
    setResponseStatus(null);

    try {
      let body: BodyInit;
      let contentType: string | undefined;

      if (file) {
        const fd = new FormData();
        fd.append("file", file);
        if (isChat && chatPrompt.trim()) fd.append("prompt", chatPrompt);
        if (!isChat && jsonBody.trim()) fd.append("json", jsonBody);
        body = fd;
        // Don't set Content-Type — browser fills boundary in.
      } else if (isChat) {
        body = JSON.stringify({ inputs: chatPrompt });
        contentType = "application/json";
      } else {
        // Validate the JSON before sending so the user gets a clear error.
        try {
          JSON.parse(jsonBody);
        } catch (parseErr) {
          throw new Error(parseErr instanceof Error ? parseErr.message : "Invalid JSON");
        }
        body = jsonBody;
        contentType = "application/json";
      }

      const res = await callInference(projectId, envId, name, body, contentType);
      const text = await res.text();
      setResponseStatus(res.status);
      // Pretty-print if it looks like JSON.
      if (!rawJson) {
        try {
          const parsed = JSON.parse(text);
          setResponse(JSON.stringify(parsed, null, 2));
        } catch {
          setResponse(text);
        }
      } else {
        setResponse(text);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Inference failed");
    } finally {
      setLoading(false);
    }
  }

  function onFileChange(e: ChangeEvent<HTMLInputElement>) {
    setFile(e.target.files?.[0] ?? null);
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      {!ready && (
        <div className="rounded-lg border border-yellow-200 bg-yellow-50 px-4 py-3 text-sm text-yellow-800">
          Model is not ready yet. Inference is disabled until the InferenceService becomes Ready.
        </div>
      )}

      {isChat ? (
        <div>
          <label className="block text-sm font-medium text-gray-700">Prompt</label>
          <textarea
            value={chatPrompt}
            onChange={(e) => setChatPrompt(e.target.value)}
            rows={4}
            placeholder="Ask anything…"
            className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      ) : (
        <div>
          <div className="flex items-center justify-between">
            <label className="block text-sm font-medium text-gray-700">Request body (JSON)</label>
            <button
              type="button"
              onClick={() => setJsonBody(sampleFor(modelType))}
              className="text-xs text-blue-600 hover:text-blue-700"
            >
              Reset to sample
            </button>
          </div>
          <textarea
            value={jsonBody}
            onChange={(e) => setJsonBody(e.target.value)}
            rows={8}
            spellCheck={false}
            className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 font-mono text-xs text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      )}

      {supportsFile && (
        <div>
          <label className="block text-sm font-medium text-gray-700">
            File <span className="font-normal text-gray-400">(optional)</span>
          </label>
          <input
            type="file"
            accept={fileHint.accept}
            onChange={onFileChange}
            className="mt-1 block w-full text-sm text-gray-700 file:mr-3 file:rounded-md file:border-0 file:bg-gray-100 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-gray-700 hover:file:bg-gray-200"
          />
          <p className="mt-1 text-xs text-gray-400">{fileHint.hint}</p>
        </div>
      )}

      <div className="flex items-center justify-between">
        <label className="flex items-center gap-2 text-xs text-gray-500">
          <input
            type="checkbox"
            checked={rawJson}
            onChange={(e) => setRawJson(e.target.checked)}
            className="h-3.5 w-3.5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
          Show raw response (no JSON pretty-print)
        </label>
        <button
          type="submit"
          disabled={!ready || loading}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
        >
          {loading ? <><Spinner size="sm" /> Sending...</> : "Send"}
        </button>
      </div>

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}

      {response !== null && (
        <div>
          <div className="flex items-center justify-between">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">
              Response
              {responseStatus && (
                <span className={`ml-2 font-mono ${responseStatus >= 200 && responseStatus < 300 ? "text-green-600" : "text-red-600"}`}>
                  {responseStatus}
                </span>
              )}
            </p>
          </div>
          <pre className="mt-1 max-h-96 overflow-auto rounded-lg border border-gray-200 bg-gray-50 p-3 font-mono text-xs text-gray-900">
            {response}
          </pre>
        </div>
      )}
    </form>
  );
}
