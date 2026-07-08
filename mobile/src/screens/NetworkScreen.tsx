import React, { useEffect, useState } from 'react';
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  TouchableOpacity,
  Alert,
  ActivityIndicator,
} from 'react-native';
import { useAgentStore } from '../store/agentStore';
import { COLORS, SPACING, FONT_SIZE, BORDER_RADIUS } from '../utils/theme';
import { ActionButton, EmptyState } from '../components/Common';
import type { NetworkStatus, TrialPool } from '../types';

export default function NetworkScreen() {
  const {
    currentAgent,
    networkStatus,
    isLoading,
    fetchNetworkStatus,
    joinNetwork,
    leaveNetwork,
  } = useAgentStore();

  const [trialPool, setTrialPool] = useState<TrialPool | null>(null);

  useEffect(() => {
    if (currentAgent) {
      fetchNetworkStatus();
    }
  }, [currentAgent]);

  const handleJoin = () => {
    Alert.alert('加入共享网络', '加入后将共享你的算力资源以获取积分，确定加入吗？', [
      { text: '取消', style: 'cancel' },
      { text: '加入', onPress: () => joinNetwork() },
    ]);
  };

  const handleLeave = () => {
    Alert.alert('退出共享网络', '退出后将不再共享算力，确定退出吗？', [
      { text: '取消', style: 'cancel' },
      { text: '退出', style: 'destructive', onPress: () => leaveNetwork() },
    ]);
  };

  if (!currentAgent) {
    return (
      <View style={styles.container}>
        <EmptyState title="暂无 Agent 配置" description="请先配置 Agent 以查看网络状态" />
      </View>
    );
  }

  if (isLoading && !networkStatus) {
    return (
      <View style={[styles.container, styles.center]}>
        <ActivityIndicator color={COLORS.primary} size="large" />
      </View>
    );
  }

  const isShared = networkStatus?.mode === 'shared';

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <Text style={styles.pageTitle}>🌐 共享网络</Text>

      {/* Mode Card */}
      <View style={[styles.modeCard, isShared ? styles.modeShared : styles.modePersonal]}>
        <View style={styles.modeHeader}>
          <Text style={styles.modeEmoji}>{isShared ? '🌍' : '👤'}</Text>
          <View>
            <Text style={styles.modeTitle}>{isShared ? '共享模式' : '个人模式'}</Text>
            <Text style={styles.modeDesc}>
              {isShared ? '已加入共享网络，算力共享中' : '仅使用个人资源'}
            </Text>
          </View>
        </View>

        <View style={styles.modeAction}>
          {isShared ? (
            <ActionButton title="退出共享网络" onPress={handleLeave} variant="danger" />
          ) : (
            <ActionButton title="加入共享网络" onPress={handleJoin} />
          )}
        </View>
      </View>

      {/* Network Stats */}
      {networkStatus && (
        <>
          <Text style={styles.sectionTitle}>网络状态</Text>
          <View style={styles.statsGrid}>
            <View style={styles.statsCard}>
              <Text style={styles.statsEmoji}>🔓</Text>
              <Text style={styles.statsValue}>{networkStatus.nodeCount || 0}</Text>
              <Text style={styles.statsLabel}>在线节点</Text>
            </View>
            <View style={styles.statsCard}>
              <Text style={styles.statsEmoji}>⭐</Text>
              <Text style={styles.statsValue}>{networkStatus.totalContributions || 0}</Text>
              <Text style={styles.statsLabel}>贡献积分</Text>
            </View>
            <View style={styles.statsCard}>
              <Text style={styles.statsEmoji}>🛡</Text>
              <Text style={styles.statsValue}>{networkStatus.reputationScore || 0}</Text>
              <Text style={styles.statsLabel}>信誉分数</Text>
            </View>
            <View style={styles.statsCard}>
              <Text style={styles.statsEmoji}>🔑</Text>
              <Text style={styles.statsValue}>{networkStatus.unlockedModels?.length || 0}</Text>
              <Text style={styles.statsLabel}>已解锁模型</Text>
            </View>
          </View>

          {/* Unlocked Models */}
          {networkStatus.unlockedModels && networkStatus.unlockedModels.length > 0 && (
            <>
              <Text style={styles.sectionTitle}>已解锁模型</Text>
              <View style={styles.modelList}>
                {networkStatus.unlockedModels.map((model, idx) => (
                  <View key={idx} style={styles.modelChip}>
                    <Text style={styles.modelChipText}>{model}</Text>
                  </View>
                ))}
              </View>
            </>
          )}
        </>
      )}

      {/* Trial Pool */}
      <Text style={styles.sectionTitle}>试用池</Text>
      <View style={styles.trialCard}>
        {trialPool ? (
          <>
            <View style={styles.trialRow}>
              <Text style={styles.trialLabel}>可用额度</Text>
              <Text style={styles.trialValue}>{trialPool.availableCredits}</Text>
            </View>
            <View style={styles.trialProgress}>
              <View
                style={[
                  styles.trialProgressFill,
                  {
                    width: `${(trialPool.usedCredits / trialPool.totalCredits) * 100}%`,
                  },
                ]}
              />
            </View>
            <View style={styles.trialRow}>
              <Text style={styles.trialSubText}>已使用: {trialPool.usedCredits} / {trialPool.totalCredits}</Text>
              <Text style={styles.trialSubText}>过期: {trialPool.expiresAt}</Text>
            </View>
          </>
        ) : (
          <Text style={styles.trialEmpty}>暂无试用池信息</Text>
        )}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: COLORS.background },
  center: { justifyContent: 'center', alignItems: 'center' },
  content: { padding: SPACING.md, paddingBottom: SPACING.xl },
  pageTitle: { fontSize: FONT_SIZE.xxl, fontWeight: '700', color: COLORS.text, marginBottom: SPACING.lg },

  modeCard: {
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.lg,
    marginBottom: SPACING.md,
  },
  modeShared: { backgroundColor: COLORS.primary + '15', borderWidth: 1, borderColor: COLORS.primary },
  modePersonal: { backgroundColor: COLORS.surface, borderWidth: 1, borderColor: COLORS.border },
  modeHeader: { flexDirection: 'row', alignItems: 'center', gap: SPACING.md, marginBottom: SPACING.md },
  modeEmoji: { fontSize: 36 },
  modeTitle: { fontSize: FONT_SIZE.xl, fontWeight: '700', color: COLORS.text },
  modeDesc: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary },
  modeAction: { marginTop: SPACING.sm },

  sectionTitle: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text, marginTop: SPACING.lg, marginBottom: SPACING.md },

  statsGrid: { flexDirection: 'row', flexWrap: 'wrap', gap: SPACING.sm },
  statsCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.md,
    padding: SPACING.md,
    alignItems: 'center',
    width: '47%',
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  statsEmoji: { fontSize: 24, marginBottom: 4 },
  statsValue: { fontSize: FONT_SIZE.xl, fontWeight: '700', color: COLORS.text },
  statsLabel: { fontSize: FONT_SIZE.xs, color: COLORS.textSecondary, marginTop: 2 },

  modelList: { flexDirection: 'row', flexWrap: 'wrap', gap: SPACING.sm },
  modelChip: {
    backgroundColor: COLORS.primary + '15',
    paddingHorizontal: SPACING.md,
    paddingVertical: SPACING.sm,
    borderRadius: BORDER_RADIUS.full,
  },
  modelChipText: { fontSize: FONT_SIZE.sm, color: COLORS.primary, fontWeight: '500' },

  trialCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  trialRow: { flexDirection: 'row', justifyContent: 'space-between', marginBottom: SPACING.sm },
  trialLabel: { fontSize: FONT_SIZE.md, color: COLORS.textSecondary },
  trialValue: { fontSize: FONT_SIZE.lg, fontWeight: '700', color: COLORS.success },
  trialProgress: {
    height: 8,
    backgroundColor: COLORS.border,
    borderRadius: 4,
    overflow: 'hidden',
    marginBottom: SPACING.sm,
  },
  trialProgressFill: { height: '100%', backgroundColor: COLORS.success, borderRadius: 4 },
  trialSubText: { fontSize: FONT_SIZE.xs, color: COLORS.textSecondary },
  trialEmpty: { fontSize: FONT_SIZE.md, color: COLORS.textSecondary, textAlign: 'center', paddingVertical: SPACING.md },
});
