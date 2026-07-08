import React, { useEffect, useState } from 'react';
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  ActivityIndicator,
  Dimensions,
} from 'react-native';
import { useAgentStore } from '../store/agentStore';
import { COLORS, SPACING, FONT_SIZE, BORDER_RADIUS } from '../utils/theme';
import { StatCard, EmptyState } from '../components/Common';
import type { UsageStats } from '../types';

const screenWidth = Dimensions.get('window').width;

export default function StatsScreen() {
  const { usageStats, fetchUsageStats, isLoading, currentAgent } = useAgentStore();
  const [stats, setStats] = useState<UsageStats | null>(null);

  useEffect(() => {
    if (currentAgent) {
      fetchUsageStats().then(() => {
        const state = useAgentStore.getState();
        setStats(state.usageStats);
      });
    }
  }, [currentAgent]);

  if (!currentAgent) {
    return (
      <View style={styles.container}>
        <EmptyState
          title="暂无 Agent 配置"
          description="请先配置 Agent 以查看统计数据"
        />
      </View>
    );
  }

  if (isLoading && !stats) {
    return (
      <View style={[styles.container, styles.center]}>
        <ActivityIndicator color={COLORS.primary} size="large" />
      </View>
    );
  }

  const todayInput = stats?.today?.inputTokens || 0;
  const todayOutput = stats?.today?.outputTokens || 0;
  const totalTokens = stats?.totalTokens || 0;
  const totalRequests = stats?.totalRequests || 0;
  const credits = stats?.contributionCredits || 0;

  // Simple bar chart using Views
  const maxTokens = Math.max(
    ...(stats?.history || []).map((d) => d.inputTokens + d.outputTokens),
    1
  );

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <Text style={styles.pageTitle}>📊 使用统计</Text>

      {/* Today's Stats */}
      <Text style={styles.sectionTitle}>今日概览</Text>
      <View style={styles.statRow}>
        <StatCard title="输入 Token" value={todayInput.toLocaleString()} color={COLORS.primary} />
        <StatCard title="输出 Token" value={todayOutput.toLocaleString()} color={COLORS.secondary} />
      </View>
      <View style={styles.statRow}>
        <StatCard title="总请求数" value={totalRequests} color={COLORS.info} />
        <StatCard title="贡献积分" value={credits} color={COLORS.success} />
      </View>

      {/* History Chart */}
      {stats?.history && stats.history.length > 0 && (
        <>
          <Text style={styles.sectionTitle}>历史趋势</Text>
          <View style={styles.chartCard}>
            <View style={styles.chartContainer}>
              {stats.history.slice(-7).map((day, idx) => {
                const total = day.inputTokens + day.outputTokens;
                const heightPercent = (total / maxTokens) * 100;
                const inputPercent = (day.inputTokens / total) * 100;
                return (
                  <View key={idx} style={styles.barGroup}>
                    <View style={styles.barContainer}>
                      <View
                        style={[
                          styles.bar,
                          {
                            height: `${Math.max(heightPercent, 5)}%`,
                            maxHeight: 120,
                          },
                        ]}
                      >
                        <View
                          style={[
                            styles.barOutput,
                            { flex: 1, backgroundColor: COLORS.primary },
                          ]}
                        />
                        <View
                          style={[
                            styles.barInput,
                            { flex: inputPercent / 100, backgroundColor: COLORS.secondary },
                          ]}
                        />
                      </View>
                    </View>
                    <Text style={styles.barLabel}>
                      {day.date.slice(5)}
                    </Text>
                    <Text style={styles.barValue}>{total.toLocaleString()}</Text>
                  </View>
                );
              })}
            </View>
            <View style={styles.chartLegend}>
              <View style={styles.legendItem}>
                <View style={[styles.legendDot, { backgroundColor: COLORS.primary }]} />
                <Text style={styles.legendText}>输入</Text>
              </View>
              <View style={styles.legendItem}>
                <View style={[styles.legendDot, { backgroundColor: COLORS.secondary }]} />
                <Text style={styles.legendText}>输出</Text>
              </View>
            </View>
          </View>
        </>
      )}

      {/* Summary */}
      <Text style={styles.sectionTitle}>累计统计</Text>
      <View style={styles.summaryCard}>
        <View style={styles.summaryRow}>
          <Text style={styles.summaryLabel}>总消耗 Token</Text>
          <Text style={styles.summaryValue}>{totalTokens.toLocaleString()}</Text>
        </View>
        <View style={styles.divider} />
        <View style={styles.summaryRow}>
          <Text style={styles.summaryLabel}>总请求次数</Text>
          <Text style={styles.summaryValue}>{totalRequests}</Text>
        </View>
        <View style={styles.divider} />
        <View style={styles.summaryRow}>
          <Text style={styles.summaryLabel}>贡献积分</Text>
          <Text style={[styles.summaryValue, { color: COLORS.success }]}>{credits}</Text>
        </View>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: COLORS.background },
  center: { justifyContent: 'center', alignItems: 'center' },
  content: { padding: SPACING.md, paddingBottom: SPACING.xl },
  pageTitle: { fontSize: FONT_SIZE.xxl, fontWeight: '700', color: COLORS.text, marginBottom: SPACING.lg },
  sectionTitle: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text, marginTop: SPACING.lg, marginBottom: SPACING.md },

  statRow: { flexDirection: 'row', gap: SPACING.sm, marginBottom: SPACING.sm },

  chartCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  chartContainer: { flexDirection: 'row', justifyContent: 'space-around', alignItems: 'flex-end', height: 160 },
  barGroup: { alignItems: 'center', flex: 1 },
  barContainer: { height: 120, justifyContent: 'flex-end', width: '60%' },
  bar: { width: '100%', borderRadius: 4, overflow: 'hidden' },
  barOutput: {},
  barInput: {},
  barLabel: { fontSize: 10, color: COLORS.textSecondary, marginTop: 4 },
  barValue: { fontSize: 9, color: COLORS.textSecondary },
  chartLegend: { flexDirection: 'row', justifyContent: 'center', gap: SPACING.md, marginTop: SPACING.md },
  legendItem: { flexDirection: 'row', alignItems: 'center', gap: 4 },
  legendDot: { width: 8, height: 8, borderRadius: 4 },
  legendText: { fontSize: FONT_SIZE.xs, color: COLORS.textSecondary },

  summaryCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  summaryRow: { flexDirection: 'row', justifyContent: 'space-between', paddingVertical: SPACING.md },
  summaryLabel: { fontSize: FONT_SIZE.md, color: COLORS.textSecondary },
  summaryValue: { fontSize: FONT_SIZE.lg, fontWeight: '700', color: COLORS.text },
  divider: { height: 1, backgroundColor: COLORS.border },
});
