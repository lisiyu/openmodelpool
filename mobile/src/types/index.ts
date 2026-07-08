// Agent 配置
export interface Agent {
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
  isActive: boolean;
  lastUsed: number;
  createdAt: number;
}

// API 消息
export interface Message {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

// Chat 请求
export interface ChatRequest {
  model: string;
  messages: Message[];
  temperature?: number;
  max_tokens?: number;
  stream?: boolean;
}

// Chat 响应
export interface ChatResponse {
  id: string;
  object: string;
  created: number;
  model: string;
  choices: ChatChoice[];
  usage: TokenUsage;
}

export interface ChatChoice {
  index: number;
  message: Message;
  finish_reason: string;
}

export interface TokenUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

// 网络状态
export interface NetworkStatus {
  mode: 'personal' | 'shared';
  nodeCount: number;
  unlockedModels: string[];
  totalContributions: number;
  reputationScore: number;
}

// 试用池信息
export interface TrialPool {
  availableCredits: number;
  usedCredits: number;
  totalCredits: number;
  expiresAt: string;
}

// 密钥
export interface ApiKey {
  id: string;
  key: string;
  name: string;
  createdAt: string;
  lastUsed: string;
  isActive: boolean;
}

// 统计
export interface UsageStats {
  today: DailyStats;
  history: DailyStats[];
  totalTokens: number;
  totalRequests: number;
  contributionCredits: number;
}

export interface DailyStats {
  date: string;
  inputTokens: number;
  outputTokens: number;
  requests: number;
}

// 模型
export interface Model {
  id: string;
  name: string;
  provider: string;
  isUnlocked: boolean;
}

// 分享
export interface ShareLink {
  url: string;
  trialKey?: string;
  expiresAt: string;
  createdAt: string;
}
