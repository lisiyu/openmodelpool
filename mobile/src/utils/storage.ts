import AsyncStorage from '@react-native-async-storage/async-storage';
import type { Agent } from '../types';

const KEYS = {
  AGENTS: '@modelmux_agents',
  CURRENT_AGENT_ID: '@modelmux_current_agent_id',
  CHAT_HISTORY: '@modelmux_chat_history',
  THEME: '@modelmux_theme',
} as const;

export const Storage = {
  // Agents
  async saveAgents(agents: Agent[]): Promise<void> {
    await AsyncStorage.setItem(KEYS.AGENTS, JSON.stringify(agents));
  },

  async getAgents(): Promise<Agent[]> {
    const raw = await AsyncStorage.getItem(KEYS.AGENTS);
    return raw ? JSON.parse(raw) : [];
  },

  async addAgent(agent: Agent): Promise<void> {
    const agents = await this.getAgents();
    agents.push(agent);
    await this.saveAgents(agents);
  },

  async removeAgent(id: string): Promise<void> {
    const agents = await this.getAgents();
    await this.saveAgents(agents.filter((a) => a.id !== id));
  },

  async updateAgent(agent: Agent): Promise<void> {
    const agents = await this.getAgents();
    const idx = agents.findIndex((a) => a.id === agent.id);
    if (idx >= 0) {
      agents[idx] = agent;
      await this.saveAgents(agents);
    }
  },

  // Current agent
  async setCurrentAgentId(id: string): Promise<void> {
    await AsyncStorage.setItem(KEYS.CURRENT_AGENT_ID, id);
  },

  async getCurrentAgentId(): Promise<string | null> {
    return AsyncStorage.getItem(KEYS.CURRENT_AGENT_ID);
  },

  // Theme
  async setTheme(theme: 'light' | 'dark' | 'system'): Promise<void> {
    await AsyncStorage.setItem(KEYS.THEME, theme);
  },

  async getTheme(): Promise<'light' | 'dark' | 'system'> {
    const t = await AsyncStorage.getItem(KEYS.THEME);
    return (t as any) || 'system';
  },
};
