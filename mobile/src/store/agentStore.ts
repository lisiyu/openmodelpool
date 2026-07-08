import { create } from 'zustand';
import type { Agent, ChatResponse, Message, Model, NetworkStatus, UsageStats, ApiKey } from '../types';
import { ModelMuxAPI } from '../services/api';
import { Storage } from '../utils/storage';

interface AgentState {
  agents: Agent[];
  currentAgent: Agent | null;
  models: Model[];
  networkStatus: NetworkStatus | null;
  usageStats: UsageStats | null;
  keys: ApiKey[];
  isLoading: boolean;
  error: string | null;

  // Chat
  chatMessages: Message[];
  chatResponse: ChatResponse | null;
  chatLoading: boolean;

  // Actions
  loadAgents: () => Promise<void>;
  addAgent: (agent: Agent) => Promise<void>;
  removeAgent: (id: string) => Promise<void>;
  setCurrentAgent: (agent: Agent) => Promise<void>;
  testConnection: (agent: Agent) => Promise<{ ok: boolean; latencyMs: number }>;

  // Chat actions
  sendChat: (model: string, messages: Message[]) => Promise<void>;
  clearChat: () => void;

  // Network
  fetchNetworkStatus: () => Promise<void>;
  joinNetwork: () => Promise<void>;
  leaveNetwork: () => Promise<void>;

  // Stats
  fetchUsageStats: () => Promise<void>;

  // Keys
  fetchKeys: () => Promise<void>;
  generateKey: (name: string) => Promise<void>;
  deleteKey: (id: string) => Promise<void>;

  // Models
  fetchModels: () => Promise<void>;

  // Error
  setError: (err: string | null) => void;
}

function getApi(agent: Agent | null): ModelMuxAPI {
  if (!agent) throw new Error('No agent selected');
  return new ModelMuxAPI(agent.baseUrl, agent.apiKey);
}

export const useAgentStore = create<AgentState>((set, get) => ({
  agents: [],
  currentAgent: null,
  models: [],
  networkStatus: null,
  usageStats: null,
  keys: [],
  isLoading: false,
  error: null,
  chatMessages: [],
  chatResponse: null,
  chatLoading: false,

  loadAgents: async () => {
    const agents = await Storage.getAgents();
    const currentId = await Storage.getCurrentAgentId();
    const current = agents.find((a) => a.id === currentId) || null;
    set({ agents, currentAgent: current });
  },

  addAgent: async (agent: Agent) => {
    await Storage.addAgent(agent);
    await get().loadAgents();
  },

  removeAgent: async (id: string) => {
    await Storage.removeAgent(id);
    const state = get();
    if (state.currentAgent?.id === id) {
      await Storage.setCurrentAgentId('');
      set({ currentAgent: null });
    }
    await get().loadAgents();
  },

  setCurrentAgent: async (agent: Agent) => {
    await Storage.setCurrentAgentId(agent.id);
    set({ currentAgent: agent });
  },

  testConnection: async (agent: Agent) => {
    const api = getApi(agent);
    return api.testConnection();
  },

  sendChat: async (model: string, messages: Message[]) => {
    const { currentAgent } = get();
    if (!currentAgent) throw new Error('No agent selected');
    set({ chatLoading: true, error: null });
    try {
      const api = getApi(currentAgent);
      const response = await api.chat(model, messages);
      set({ chatResponse: response, chatLoading: false });
    } catch (e: any) {
      set({ error: e.message, chatLoading: false });
    }
  },

  clearChat: () => set({ chatMessages: [], chatResponse: null }),

  fetchNetworkStatus: async () => {
    set({ isLoading: true });
    try {
      const api = getApi(get().currentAgent);
      const status = await api.getNetworkStatus();
      set({ networkStatus: status, isLoading: false });
    } catch (e: any) {
      set({ error: e.message, isLoading: false });
    }
  },

  joinNetwork: async () => {
    try {
      const api = getApi(get().currentAgent);
      await api.joinNetwork();
      await get().fetchNetworkStatus();
    } catch (e: any) {
      set({ error: e.message });
    }
  },

  leaveNetwork: async () => {
    try {
      const api = getApi(get().currentAgent);
      await api.leaveNetwork();
      await get().fetchNetworkStatus();
    } catch (e: any) {
      set({ error: e.message });
    }
  },

  fetchUsageStats: async () => {
    set({ isLoading: true });
    try {
      const api = getApi(get().currentAgent);
      const stats = await api.getUsageStats();
      set({ usageStats: stats, isLoading: false });
    } catch (e: any) {
      set({ error: e.message, isLoading: false });
    }
  },

  fetchKeys: async () => {
    set({ isLoading: true });
    try {
      const api = getApi(get().currentAgent);
      const keys = await api.getKeys();
      set({ keys, isLoading: false });
    } catch (e: any) {
      set({ error: e.message, isLoading: false });
    }
  },

  generateKey: async (name: string) => {
    try {
      const api = getApi(get().currentAgent);
      await api.generateKey(name);
      await get().fetchKeys();
    } catch (e: any) {
      set({ error: e.message });
    }
  },

  deleteKey: async (id: string) => {
    try {
      const api = getApi(get().currentAgent);
      await api.deleteKey(id);
      await get().fetchKeys();
    } catch (e: any) {
      set({ error: e.message });
    }
  },

  fetchModels: async () => {
    try {
      const api = getApi(get().currentAgent);
      const models = await api.getModels();
      set({ models });
    } catch (e: any) {
      set({ error: e.message });
    }
  },

  setError: (err) => set({ error: err }),
}));
