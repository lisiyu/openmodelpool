import type { Message, ChatResponse, NetworkStatus, TrialPool, ApiKey, UsageStats, Model, ShareLink } from '../types';

export class ModelMuxAPI {
  private baseUrl: string;
  private apiKey: string;

  constructor(baseUrl: string, apiKey: string) {
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.apiKey = apiKey;
  }

  private get headers(): Record<string, string> {
    return {
      Authorization: `Bearer ${this.apiKey}`,
      'Content-Type': 'application/json',
    };
  }

  // =====================
  // Chat
  // =====================
  async chat(model: string, messages: Message[], options?: { temperature?: number; max_tokens?: number }): Promise<ChatResponse> {
    const body: Record<string, unknown> = { model, messages };
    if (options?.temperature !== undefined) body.temperature = options.temperature;
    if (options?.max_tokens !== undefined) body.max_tokens = options.max_tokens;

    const response = await fetch(`${this.baseUrl}/v1/chat/completions`, {
      method: 'POST',
      headers: this.headers,
      body: JSON.stringify(body),
    });

    if (!response.ok) {
      const err = await response.json().catch(() => ({ error: response.statusText }));
      throw new Error(err.error?.message || `API error: ${response.status}`);
    }

    return response.json();
  }

  // =====================
  // Models
  // =====================
  async getModels(): Promise<Model[]> {
    const response = await fetch(`${this.baseUrl}/v1/models`, {
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to fetch models: ${response.status}`);
    const data = await response.json();
    return data.data?.map((m: any) => ({
      id: m.id,
      name: m.id,
      provider: m.owned_by || 'unknown',
      isUnlocked: true,
    })) ?? [];
  }

  // =====================
  // Network
  // =====================
  async getNetworkStatus(): Promise<NetworkStatus> {
    const response = await fetch(`${this.baseUrl}/api/network/status`, {
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to fetch network status: ${response.status}`);
    return response.json();
  }

  async joinNetwork(): Promise<void> {
    const response = await fetch(`${this.baseUrl}/api/network/join`, {
      method: 'POST',
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to join network: ${response.status}`);
  }

  async leaveNetwork(): Promise<void> {
    const response = await fetch(`${this.baseUrl}/api/network/leave`, {
      method: 'POST',
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to leave network: ${response.status}`);
  }

  // =====================
  // Trial Pool
  // =====================
  async getTrialPool(): Promise<TrialPool> {
    const response = await fetch(`${this.baseUrl}/api/trial/pool`, {
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to fetch trial pool: ${response.status}`);
    return response.json();
  }

  // =====================
  // Keys
  // =====================
  async getKeys(): Promise<ApiKey[]> {
    const response = await fetch(`${this.baseUrl}/api/keys`, {
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to fetch keys: ${response.status}`);
    return response.json();
  }

  async generateKey(name: string): Promise<ApiKey> {
    const response = await fetch(`${this.baseUrl}/api/keys`, {
      method: 'POST',
      headers: this.headers,
      body: JSON.stringify({ name }),
    });
    if (!response.ok) throw new Error(`Failed to generate key: ${response.status}`);
    return response.json();
  }

  async deleteKey(keyId: string): Promise<void> {
    const response = await fetch(`${this.baseUrl}/api/keys/${keyId}`, {
      method: 'DELETE',
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to delete key: ${response.status}`);
  }

  // =====================
  // Stats
  // =====================
  async getUsageStats(): Promise<UsageStats> {
    const response = await fetch(`${this.baseUrl}/api/stats/usage`, {
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to fetch usage stats: ${response.status}`);
    return response.json();
  }

  // =====================
  // Share
  // =====================
  async generateShareLink(): Promise<ShareLink> {
    const response = await fetch(`${this.baseUrl}/api/share/link`, {
      method: 'POST',
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to generate share link: ${response.status}`);
    return response.json();
  }

  async shareTrialKey(keyId: string): Promise<ShareLink> {
    const response = await fetch(`${this.baseUrl}/api/share/trial/${keyId}`, {
      method: 'POST',
      headers: this.headers,
    });
    if (!response.ok) throw new Error(`Failed to share trial key: ${response.status}`);
    return response.json();
  }

  // =====================
  // Health
  // =====================
  async testConnection(): Promise<{ ok: boolean; latencyMs: number }> {
    const start = Date.now();
    try {
      const response = await fetch(`${this.baseUrl}/v1/models`, {
        headers: this.headers,
      });
      const latencyMs = Date.now() - start;
      return { ok: response.ok, latencyMs };
    } catch {
      return { ok: false, latencyMs: Date.now() - start };
    }
  }
}
