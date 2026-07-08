import React, { useState } from 'react';
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  TouchableOpacity,
  TextInput,
  Alert,
  ActivityIndicator,
  FlatList,
} from 'react-native';
import { useAgentStore } from '../store/agentStore';
import { COLORS, SPACING, FONT_SIZE, BORDER_RADIUS } from '../utils/theme';
import { StatCard, ActionButton, EmptyState } from '../components/Common';
import type { Message, Model } from '../types';

export default function HomeScreen({ navigation }: any) {
  const {
    agents,
    currentAgent,
    models,
    chatLoading,
    chatResponse,
    error,
    sendChat,
    clearChat,
    loadAgents,
    fetchModels,
    setError,
  } = useAgentStore();

  const [inputText, setInputText] = useState('');
  const [selectedModel, setSelectedModel] = useState('');
  const [showModelPicker, setShowModelPicker] = useState(false);

  React.useEffect(() => {
    loadAgents();
  }, []);

  React.useEffect(() => {
    if (currentAgent) {
      fetchModels().then(() => {
        const store = useAgentStore.getState();
        if (store.models.length > 0 && !selectedModel) {
          setSelectedModel(store.models[0].id);
        }
      });
    }
  }, [currentAgent]);

  const handleSend = async () => {
    if (!inputText.trim() || !currentAgent) {
      Alert.alert('提示', '请先配置 Agent 连接');
      return;
    }
    const messages: Message[] = [{ role: 'user', content: inputText.trim() }];
    await sendChat(selectedModel || 'gpt-3.5-turbo', messages);
    setInputText('');
  };

  if (!currentAgent) {
    return (
      <View style={styles.container}>
        <EmptyState
          title="暂无 Agent 配置"
          description="请先添加一个 Agent 实例来开始使用"
          actionLabel="去配置"
          onAction={() => navigation.navigate('配置')}
        />
      </View>
    );
  }

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      {/* Quick Access */}
      <View style={styles.header}>
        <Text style={styles.greeting}>👋 你好</Text>
        <Text style={styles.agentName}>{currentAgent.name}</Text>
      </View>

      {/* Model Selector */}
      <TouchableOpacity
        style={styles.modelSelector}
        onPress={() => setShowModelPicker(!showModelPicker)}
      >
        <Text style={styles.modelSelectorLabel}>模型</Text>
        <Text style={styles.modelSelectorValue}>{selectedModel || '选择模型'} ▾</Text>
      </TouchableOpacity>

      {showModelPicker && (
        <View style={styles.modelPickerDropdown}>
          {models.map((m) => (
            <TouchableOpacity
              key={m.id}
              style={[
                styles.modelOption,
                m.id === selectedModel && styles.modelOptionActive,
              ]}
              onPress={() => {
                setSelectedModel(m.id);
                setShowModelPicker(false);
              }}
            >
              <Text style={[styles.modelOptionText, m.id === selectedModel && styles.modelOptionTextActive]}>
                {m.name}
              </Text>
              <Text style={styles.modelOptionProvider}>{m.provider}</Text>
            </TouchableOpacity>
          ))}
        </View>
      )}

      {/* Chat Input */}
      <View style={styles.chatCard}>
        <Text style={styles.cardTitle}>💬 快速测试</Text>
        <TextInput
          style={styles.input}
          placeholder="输入你的 Prompt..."
          placeholderTextColor={COLORS.textSecondary}
          value={inputText}
          onChangeText={setInputText}
          multiline
          numberOfLines={4}
        />
        <View style={styles.chatActions}>
          <ActionButton
            title={chatLoading ? '处理中...' : '发送'}
            onPress={handleSend}
            disabled={chatLoading || !inputText.trim()}
          />
          {chatResponse && (
            <ActionButton
              title="清除"
              onPress={clearChat}
              variant="secondary"
            />
          )}
        </View>

        {/* Response */}
        {chatLoading && (
          <View style={styles.loadingContainer}>
            <ActivityIndicator color={COLORS.primary} />
            <Text style={styles.loadingText}>正在等待响应...</Text>
          </View>
        )}

        {chatResponse && (
          <View style={styles.responseCard}>
            <Text style={styles.responseLabel}>响应结果</Text>
            <Text style={styles.responseText}>
              {chatResponse.choices[0]?.message.content}
            </Text>
            <View style={styles.usageRow}>
              <Text style={styles.usageText}>
                Tokens: {chatResponse.usage.total_tokens}
                (输入 {chatResponse.usage.prompt_tokens} + 输出 {chatResponse.usage.completion_tokens})
              </Text>
              <Text style={styles.usageModel}>{chatResponse.model}</Text>
            </View>
          </View>
        )}
      </View>

      {/* Error */}
      {error && (
        <View style={styles.errorCard}>
          <Text style={styles.errorText}>⚠️ {error}</Text>
        </View>
      )}

      {/* Quick Actions */}
      <View style={styles.quickActions}>
        <Text style={styles.sectionTitle}>快捷操作</Text>
        <View style={styles.quickActionsRow}>
          <TouchableOpacity
            style={styles.quickActionBtn}
            onPress={() => navigation.navigate('配置')}
          >
            <Text style={styles.quickActionIcon}>🔑</Text>
            <Text style={styles.quickActionLabel}>密钥管理</Text>
          </TouchableOpacity>
          <TouchableOpacity
            style={styles.quickActionBtn}
            onPress={() => navigation.navigate('网络')}
          >
            <Text style={styles.quickActionIcon}>🌐</Text>
            <Text style={styles.quickActionLabel}>共享网络</Text>
          </TouchableOpacity>
          <TouchableOpacity
            style={styles.quickActionBtn}
            onPress={() => navigation.navigate('统计')}
          >
            <Text style={styles.quickActionIcon}>📊</Text>
            <Text style={styles.quickActionLabel}>使用统计</Text>
          </TouchableOpacity>
          <TouchableOpacity
            style={styles.quickActionBtn}
            onPress={() => navigation.navigate('我的')}
          >
            <Text style={styles.quickActionIcon}>📤</Text>
            <Text style={styles.quickActionLabel}>分享</Text>
          </TouchableOpacity>
        </View>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: COLORS.background },
  content: { padding: SPACING.md, paddingBottom: SPACING.xl },
  header: { marginBottom: SPACING.lg },
  greeting: { fontSize: FONT_SIZE.lg, color: COLORS.textSecondary },
  agentName: { fontSize: FONT_SIZE.xxl, fontWeight: '700', color: COLORS.text, marginTop: 4 },

  modelSelector: {
    backgroundColor: COLORS.surface,
    padding: SPACING.md,
    borderRadius: BORDER_RADIUS.md,
    marginBottom: SPACING.md,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    borderWidth: 1,
    borderColor: COLORS.border,
  },
  modelSelectorLabel: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary },
  modelSelectorValue: { fontSize: FONT_SIZE.md, fontWeight: '600', color: COLORS.primary },

  modelPickerDropdown: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.md,
    marginBottom: SPACING.md,
    borderWidth: 1,
    borderColor: COLORS.border,
    overflow: 'hidden',
  },
  modelOption: {
    padding: SPACING.md,
    borderBottomWidth: 1,
    borderBottomColor: COLORS.border,
    flexDirection: 'row',
    justifyContent: 'space-between',
  },
  modelOptionActive: { backgroundColor: COLORS.primary + '10' },
  modelOptionText: { fontSize: FONT_SIZE.md, color: COLORS.text },
  modelOptionTextActive: { color: COLORS.primary, fontWeight: '600' },
  modelOptionProvider: { fontSize: FONT_SIZE.xs, color: COLORS.textSecondary },

  chatCard: {
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
  cardTitle: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text, marginBottom: SPACING.md },
  input: {
    backgroundColor: COLORS.background,
    borderRadius: BORDER_RADIUS.sm,
    padding: SPACING.md,
    fontSize: FONT_SIZE.md,
    minHeight: 100,
    textAlignVertical: 'top',
    color: COLORS.text,
    borderWidth: 1,
    borderColor: COLORS.border,
  },
  chatActions: {
    flexDirection: 'row',
    gap: SPACING.sm,
    marginTop: SPACING.md,
  },
  loadingContainer: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: SPACING.md,
    gap: SPACING.sm,
  },
  loadingText: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary },

  responseCard: {
    marginTop: SPACING.md,
    backgroundColor: COLORS.background,
    borderRadius: BORDER_RADIUS.sm,
    padding: SPACING.md,
    borderLeftWidth: 3,
    borderLeftColor: COLORS.primary,
  },
  responseLabel: { fontSize: FONT_SIZE.sm, fontWeight: '600', color: COLORS.primary, marginBottom: SPACING.sm },
  responseText: { fontSize: FONT_SIZE.md, color: COLORS.text, lineHeight: 22 },
  usageRow: { marginTop: SPACING.sm, flexDirection: 'row', justifyContent: 'space-between' },
  usageText: { fontSize: FONT_SIZE.xs, color: COLORS.textSecondary },
  usageModel: { fontSize: FONT_SIZE.xs, color: COLORS.primary, fontWeight: '500' },

  errorCard: {
    backgroundColor: COLORS.error + '15',
    borderRadius: BORDER_RADIUS.sm,
    padding: SPACING.md,
    marginBottom: SPACING.md,
    borderLeftWidth: 3,
    borderLeftColor: COLORS.error,
  },
  errorText: { fontSize: FONT_SIZE.sm, color: COLORS.error },

  quickActions: { marginTop: SPACING.md },
  sectionTitle: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text, marginBottom: SPACING.md },
  quickActionsRow: { flexDirection: 'row', flexWrap: 'wrap', gap: SPACING.sm },
  quickActionBtn: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.md,
    padding: SPACING.md,
    alignItems: 'center',
    width: '22%',
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 1 },
    shadowOpacity: 0.05,
    shadowRadius: 4,
    elevation: 2,
  },
  quickActionIcon: { fontSize: 28, marginBottom: 4 },
  quickActionLabel: { fontSize: FONT_SIZE.xs, color: COLORS.text, fontWeight: '500' },
});
