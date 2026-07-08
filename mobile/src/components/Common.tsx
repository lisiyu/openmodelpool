import React from 'react';
import { View, Text, StyleSheet, TouchableOpacity } from 'react-native';
import { COLORS, SPACING, FONT_SIZE, BORDER_RADIUS } from '../utils/theme';

interface StatCardProps {
  title: string;
  value: string | number;
  subtitle?: string;
  color?: string;
  icon?: string;
}

export const StatCard: React.FC<StatCardProps> = ({ title, value, subtitle, color = COLORS.primary }) => (
  <View style={styles.card}>
    <View style={[styles.indicator, { backgroundColor: color }]} />
    <Text style={styles.title}>{title}</Text>
    <Text style={[styles.value, { color }]}>{value}</Text>
    {subtitle && <Text style={styles.subtitle}>{subtitle}</Text>}
  </View>
);

interface ActionButtonProps {
  title: string;
  onPress: () => void;
  variant?: 'primary' | 'secondary' | 'danger';
  disabled?: boolean;
  icon?: string;
}

export const ActionButton: React.FC<ActionButtonProps> = ({ title, onPress, variant = 'primary', disabled = false }) => {
  const bgColor = variant === 'primary' ? COLORS.primary : variant === 'danger' ? COLORS.error : COLORS.surface;
  const textColor = variant === 'secondary' ? COLORS.primary : '#FFF';

  return (
    <TouchableOpacity
      style={[styles.button, { backgroundColor: bgColor, borderColor: variant === 'secondary' ? COLORS.primary : 'transparent', borderWidth: variant === 'secondary' ? 1 : 0 }, disabled && { opacity: 0.5 }]}
      onPress={onPress}
      disabled={disabled}
    >
      <Text style={[styles.buttonText, { color: textColor }]}>{title}</Text>
    </TouchableOpacity>
  );
};

interface EmptyStateProps {
  title: string;
  description?: string;
  actionLabel?: string;
  onAction?: () => void;
}

export const EmptyState: React.FC<EmptyStateProps> = ({ title, description, actionLabel, onAction }) => (
  <View style={styles.empty}>
    <Text style={styles.emptyEmoji}>📭</Text>
    <Text style={styles.emptyTitle}>{title}</Text>
    {description && <Text style={styles.emptyDesc}>{description}</Text>}
    {actionLabel && onAction && (
      <TouchableOpacity style={styles.emptyBtn} onPress={onAction}>
        <Text style={styles.emptyBtnText}>{actionLabel}</Text>
      </TouchableOpacity>
    )}
  </View>
);

const styles = StyleSheet.create({
  card: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.md,
    padding: SPACING.md,
    marginVertical: SPACING.xs,
    marginHorizontal: SPACING.sm,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
    flex: 1,
  },
  indicator: {
    width: 32,
    height: 4,
    borderRadius: 2,
    marginBottom: SPACING.sm,
  },
  title: {
    fontSize: FONT_SIZE.xs,
    color: COLORS.textSecondary,
    marginBottom: 4,
  },
  value: {
    fontSize: FONT_SIZE.xl,
    fontWeight: '700',
  },
  subtitle: {
    fontSize: FONT_SIZE.xs,
    color: COLORS.textSecondary,
    marginTop: 2,
  },
  button: {
    paddingVertical: SPACING.md,
    paddingHorizontal: SPACING.lg,
    borderRadius: BORDER_RADIUS.md,
    alignItems: 'center',
    justifyContent: 'center',
  },
  buttonText: {
    fontSize: FONT_SIZE.md,
    fontWeight: '600',
  },
  empty: {
    alignItems: 'center',
    padding: SPACING.xl,
  },
  emptyEmoji: {
    fontSize: 48,
    marginBottom: SPACING.md,
  },
  emptyTitle: {
    fontSize: FONT_SIZE.lg,
    fontWeight: '600',
    color: COLORS.text,
    marginBottom: SPACING.sm,
    textAlign: 'center',
  },
  emptyDesc: {
    fontSize: FONT_SIZE.sm,
    color: COLORS.textSecondary,
    textAlign: 'center',
    marginBottom: SPACING.md,
  },
  emptyBtn: {
    backgroundColor: COLORS.primary,
    paddingVertical: SPACING.sm,
    paddingHorizontal: SPACING.lg,
    borderRadius: BORDER_RADIUS.md,
  },
  emptyBtnText: {
    color: '#FFF',
    fontWeight: '600',
  },
});
