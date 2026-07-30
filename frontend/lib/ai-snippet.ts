/**
 * Quickstart snippets for the AI Gateway page. The whole selling point is that
 * an OpenAI-compatible client needs exactly two changes -- base_url and api_key
 * -- so the snippets are deliberately the stock SDK call with those two lines
 * substituted, not a bespoke wrapper.
 *
 * `key` is the plaintext key when one was just minted, otherwise the
 * placeholder the caller passes in.
 */

export function pythonSnippet(baseUrl: string, key: string, model: string): string {
  return [
    "from openai import OpenAI",
    "",
    "client = OpenAI(",
    `    base_url="${baseUrl}",`,
    `    api_key="${key}",`,
    ")",
    "",
    "resp = client.chat.completions.create(",
    `    model="${model}",`,
    '    messages=[{"role": "user", "content": "Привет из России без VPN"}],',
    ")",
    "print(resp.choices[0].message.content)",
  ].join("\n");
}

export function nodeSnippet(baseUrl: string, key: string, model: string): string {
  return [
    'import OpenAI from "openai";',
    "",
    "const client = new OpenAI({",
    `  baseURL: "${baseUrl}",`,
    `  apiKey: "${key}",`,
    "});",
    "",
    "const resp = await client.chat.completions.create({",
    `  model: "${model}",`,
    '  messages: [{ role: "user", content: "Привет из России без VPN" }],',
    "});",
    "console.log(resp.choices[0].message.content);",
  ].join("\n");
}

export function curlSnippet(baseUrl: string, key: string, model: string): string {
  return [
    `curl -sS ${baseUrl}/chat/completions \\`,
    `  -H "Authorization: Bearer ${key}" \\`,
    '  -H "Content-Type: application/json" \\',
    `  -d '{"model":"${model}","messages":[{"role":"user","content":"ping"}]}'`,
  ].join("\n");
}
