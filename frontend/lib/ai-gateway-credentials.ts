export interface CredentialEditValues {
  label: string;
  apiBase: string;
  apiKey: string;
  priority: string;
}

export interface CredentialUpdatePayload {
  label: string;
  api_base: string;
  api_key?: string;
  priority: number;
}

export function buildCredentialUpdate(values: CredentialEditValues): CredentialUpdatePayload {
  const priority = Number(values.priority);
  if (!Number.isInteger(priority) || priority < 0) {
    throw new Error("priority must be a non-negative integer");
  }
  const payload: CredentialUpdatePayload = {
    label: values.label.trim(),
    api_base: values.apiBase.trim(),
    priority,
  };
  const apiKey = values.apiKey.trim();
  if (apiKey) payload.api_key = apiKey;
  return payload;
}
