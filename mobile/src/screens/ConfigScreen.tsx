import React, { useState, useEffect } from 'react';
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  TouchableOpacity,
  TextInput,
  Alert,
  Modal,
  ActivityIndicator,
} from 'react-native';
import { useAgentStore } from '../store/agentStore';
import { COLORS, SPACING, FONT_SIZE, BORDER_RADIUS } from '../utils/theme';
import { ActionButton, EmptyState } from '../components/Common';
import type { Agent } from '../types';

function generateId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2);
}

export default function ConfigScreen() {
  const { agents, currentAgent, addAgent, removeAgent, setCurrentAgent, testConnection, loadAgents } = useAgentStore();

  const [showAddModal, setShowAddModal] = useState(false);
  const [editAgent, setEditAgent] = useState<Agent | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<{ ok: boolean; latencyMs: number } | null>(null);

  // Form state
  const [name, setName] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [apiKey, setApiKey] = useState('');

  useEffect(() => {
    loadAgents();
  }, []);

  const resetForm = () => {
    setName('');
    setBaseUrl('');
    setApiKey('');
    setEditAgent(null);
  };

  const openAdd = () => {
    resetForm();
    setShowAddModal(true);
  };

  const openEdit = (agent: Agent) => {
    setEditAgent(agent);
    setName(agent.name);
    setBaseUrl(agent.baseUrl);
    setApiKey(agent.apiKey);
    setShowAddModal(true);
  };

  const handleSave = async () => {
    if (!name.trim() || !baseUrl.trim() || !apiKey.trim()) {
      Alert.alert('提示', '请填写所有字段');
      return;
    }
    const agent: Agent = {
      id: editAgent?.id || generateId(),
      name: name.trim(),
      baseUrl: baseUrl.trim(),
      apiKey: apiKey.trim(),
      isActive: true,
      lastUsed: Date.now(),
      createdAt: editAgent?.createdAt || Date.now(),
    };

    if (editAgent) {
      const { Storage } = await import('../utils/storage');
      await Storage.updateAgent(agent);
    } else {
      await addAgent(agent);
    }
    setShowAddModal(false);
    resetForm();
    await loadAgents();
  };

  const handleDelete = (agent: Agent) => {
    Alert.alert('确认删除', `确定要删除 ${agent.name} 吗？`, [
      { text: '取消', style: 'cancel' },
      {
        text: '删除',
        style: 'destructive',
        onPress: async () => {
          await removeAgent(agent.id);
          await loadAgents();
        },
      },
    ]);
  };

  const handleTest = async (agent: Agent) => {
    setTesting(agent.id);
    setTestResult(null);
    try {
      const result = await testConnection(agent);
      setTestResult(result);
      if (result.ok) {
        Alert.alert('✅ 连接成功', `延迟: ${result.latencyMs}ms`);
      } else {
        Alert.alert('❌ 连接失败', '请检查地址和密钥');
      }
    } catch (e: any) {
      Alert.alert('❌ 错误', e.message);
    }
    setTesting(null);
  };

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <Text style={styles.pageTitle}>Agent 配置</Text>

      {agents.length === 0 ? (
        <EmptyState
          title="暂无 Agent"
          description="添加你的第一个 Agent 实例"
          actionLabel="添加 Agent"
          onAction={openAdd}
        />
      ) : (
        agents.map((agent) => (
          <View
            key={agent.id}
            style={[
              styles.agentCard,
              currentAgent?.id === agent.id && styles.agentCardActive,
            ]}
          >
            <View style={styles.agentHeader}>
              <View style={styles.agentInfo}>
                <Text style={styles.agentName}>{agent.name}</Text>
                <Text style={styles.agentUrl} numberOfLines={1}>
                  {agent.baseUrl}
                </Text>
              </View>
              {currentAgent?.id === agent.id && (
                <View style={styles.activeBadge}>
                  <Text style={styles.activeBadgeText}>当前</Text>
                </View>
              )}
            </View>

            <View style={styles.agentActions}>
              <TouchableOpacity
                style={styles.actionBtn}
                onPress={() => handleTest(agent)}
                disabled={testing === agent.id}
              >
                <Text style={styles.actionBtnText}>
                  {testing === agent.id ? '⏳ 测试中...' : '🔌 测试连接'}
                </Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={styles.actionBtn}
                onPress={() => setCurrentAgent(agent)}
              >
                <Text style={styles.actionBtnText}>✅ 使用</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={styles.actionBtn}
                onPress={() => openEdit(agent)}
              >
                <Text style={styles.actionBtnText}>✏️ 编辑</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={[styles.actionBtn, styles.deleteBtn]}
                onPress={() => handleDelete(agent)}
              >
                <Text style={[styles.actionBtnText, styles.deleteBtnText]}>🗑 删除</Text>
              </TouchableOpacity>
            </View>
          </View>
        ))
      )}

      {/* Add Button */}
      <TouchableOpacity style={styles.fab} onPress={openAdd}>
        <Text style={styles.fabText}>+ 添加 Agent</Text>
      </TouchableOpacity>

      {/* Add/Edit Modal */}
      <Modal visible={showAddModal} animationType="slide" transparent>
        <View style={styles.modalOverlay}>
          <View style={styles.modalContent}>
            <Text style={styles.modalTitle}>{editAgent ? '编辑 Agent' : '添加 Agent'}</Text>

            <Text style={styles.label}>名称</Text>
            <TextInput
              style={styles.input}
              placeholder="例如：我的 Agent"
              placeholderTextColor={COLORS.textSecondary}
              value={name}
              onChangeText={setName}
            />

            <Text style={styles.label}>地址</Text>
            <TextInput
              style={styles.input}
              placeholder="https://xxx.trycloudflare.com"
              placeholderTextColor={COLORS.textSecondary}
              value={baseUrl}
              onChangeText={setBaseUrl}
              autoCapitalize="none"
              autoCorrect={false}
              keyboardType="url"
            />

            <Text style={styles.label}>API Key</Text>
            <TextInput
              style={styles.input}
              placeholder="mk_xxxxx"
              placeholderTextColor={COLORS.textSecondary}
              value={apiKey}
              onChangeText={setApiKey}
              autoCapitalize="none"
              autoCorrect={false}
              secureTextEntry
            />

            <View style={styles.modalActions}>
              <ActionButton
                title="取消"
                onPress={() => {
                  setShowAddModal(false);
                  resetForm();
                }}
                variant="secondary"
              />
              <ActionButton title="保存" onPress={handleSave} />
            </View>
          </View>
        </View>
      </Modal>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: COLORS.background },
  content: { padding: SPACING.md, paddingBottom: 100 },
  pageTitle: { fontSize: FONT_SIZE.xxl, fontWeight: '700', color: COLORS.text, marginBottom: SPACING.lg },

  agentCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.md,
    marginBottom: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  agentCardActive: {
    borderWidth: 2,
    borderColor: COLORS.primary,
  },
  agentHeader: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: SPACING.md },
  agentInfo: { flex: 1 },
  agentName: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text },
  agentUrl: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, marginTop: 4 },
  activeBadge: { backgroundColor: COLORS.primary, paddingHorizontal: 8, paddingVertical: 2, borderRadius: BORDER_RADIUS.full },
  activeBadgeText: { color: '#FFF', fontSize: FONT_SIZE.xs, fontWeight: '600' },

  agentActions: { flexDirection: 'row', flexWrap: 'wrap', gap: SPACING.sm },
  actionBtn: {
    backgroundColor: COLORS.background,
    paddingVertical: SPACING.sm,
    paddingHorizontal: SPACING.md,
    borderRadius: BORDER_RADIUS.sm,
  },
  actionBtnText: { fontSize: FONT_SIZE.sm, color: COLORS.text },
  deleteBtn: {},
  deleteBtnText: { color: COLORS.error },

  fab: {
    backgroundColor: COLORS.primary,
    paddingVertical: SPACING.md,
    borderRadius: BORDER_RADIUS.md,
    alignItems: 'center',
    marginTop: SPACING.md,
  },
  fabText: { color: '#FFF', fontSize: FONT_SIZE.md, fontWeight: '600' },

  modalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.5)',
    justifyContent: 'center',
    padding: SPACING.lg,
  },
  modalContent: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.lg,
  },
  modalTitle: { fontSize: FONT_SIZE.xl, fontWeight: '700', color: COLORS.text, marginBottom: SPACING.lg },
  label: { fontSize: FONT_SIZE.sm, fontWeight: '500', color: COLORS.text, marginBottom: SPACING.xs, marginTop: SPACING.md },
  input: {
    backgroundColor: COLORS.background,
    borderWidth: 1,
    borderColor: COLORS.border,
    borderRadius: BORDER_RADIUS.sm,
    padding: SPACING.md,
    fontSize: FONT_SIZE.md,
    color: COLORS.text,
  },
  modalActions: { flexDirection: 'row', gap: SPACING.sm, marginTop: SPACING.lg },
});
