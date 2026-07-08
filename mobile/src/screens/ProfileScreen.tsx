import React, { useState } from 'react';
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  TouchableOpacity,
  Alert,
  Modal,
  TextInput,
  Share,
  Switch,
} from 'react-native';
import { useAgentStore } from '../store/agentStore';
import { COLORS, SPACING, FONT_SIZE, BORDER_RADIUS } from '../utils/theme';
import { ActionButton, EmptyState } from '../components/Common';
import { Storage } from '../utils/storage';
import type { ApiKey } from '../types';

export default function ProfileScreen() {
  const { currentAgent, keys, fetchKeys, generateKey, deleteKey } = useAgentStore();

  const [darkMode, setDarkMode] = useState(false);
  const [showKeyModal, setShowKeyModal] = useState(false);
  const [keyName, setKeyName] = useState('');
  const [showShareModal, setShowShareModal] = useState(false);
  const [shareLink, setShareLink] = useState('');

  const handleGenerateKey = async () => {
    if (!keyName.trim()) {
      Alert.alert('提示', '请输入密钥名称');
      return;
    }
    await generateKey(keyName.trim());
    setShowKeyModal(false);
    setKeyName('');
  };

  const handleDeleteKey = (key: ApiKey) => {
    Alert.alert('删除密钥', `确定要删除密钥 "${key.name}" 吗？`, [
      { text: '取消', style: 'cancel' },
      { text: '删除', style: 'destructive', onPress: () => deleteKey(key.id) },
    ]);
  };

  const handleShare = async () => {
    if (!currentAgent) {
      Alert.alert('提示', '请先配置 Agent');
      return;
    }
    try {
      await Share.share({
        message: `我正在使用 ModelMux Agent！\n地址: ${currentAgent.baseUrl}\n一起来体验吧！`,
        title: '分享 ModelMux Agent',
      });
    } catch (e) {
      // user cancelled
    }
  };

  if (!currentAgent) {
    return (
      <View style={styles.container}>
        <EmptyState title="暂无 Agent 配置" description="请先在「配置」页面添加 Agent" />
      </View>
    );
  }

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <Text style={styles.pageTitle}>👤 我的</Text>

      {/* Profile Card */}
      <View style={styles.profileCard}>
        <View style={styles.avatar}>
          <Text style={styles.avatarText}>M</Text>
        </View>
        <View style={styles.profileInfo}>
          <Text style={styles.profileName}>ModelMux Agent</Text>
          <Text style={styles.profileUrl}>{currentAgent.baseUrl}</Text>
        </View>
      </View>

      {/* Key Management */}
      <Text style={styles.sectionTitle}>🔑 密钥管理</Text>
      <View style={styles.keyList}>
        {keys.length === 0 ? (
          <View style={styles.emptyKeys}>
            <Text style={styles.emptyKeysText}>暂无密钥</Text>
          </View>
        ) : (
          keys.map((key) => (
            <View key={key.id} style={styles.keyItem}>
              <View style={styles.keyInfo}>
                <Text style={styles.keyName}>{key.name}</Text>
                <Text style={styles.keyMasked}>
                  {key.key.slice(0, 6)}••••••{key.key.slice(-4)}
                </Text>
                <Text style={styles.keyDate}>创建: {key.createdAt}</Text>
              </View>
              <View style={styles.keyActions}>
                <TouchableOpacity
                  style={styles.keyBtn}
                  onPress={() => {
                    // Copy key
                    Alert.alert('已复制', `密钥 ${key.key.slice(0, 6)}... 已复制到剪贴板`);
                  }}
                >
                  <Text style={styles.keyBtnText}>📋</Text>
                </TouchableOpacity>
                <TouchableOpacity
                  style={styles.keyBtn}
                  onPress={() => handleDeleteKey(key)}
                >
                  <Text style={[styles.keyBtnText, { color: COLORS.error }]}>🗑</Text>
                </TouchableOpacity>
              </View>
            </View>
          ))
        )}
      </View>
      <TouchableOpacity style={styles.addKeyBtn} onPress={() => setShowKeyModal(true)}>
        <Text style={styles.addKeyBtnText}>+ 生成新密钥</Text>
      </TouchableOpacity>

      {/* Share */}
      <Text style={styles.sectionTitle}>📤 分享</Text>
      <View style={styles.shareCard}>
        <View style={styles.shareRow}>
          <View>
            <Text style={styles.shareTitle}>分享给好友</Text>
            <Text style={styles.shareDesc}>生成分享链接，邀请好友一起使用</Text>
          </View>
          <ActionButton title="分享" onPress={handleShare} />
        </View>
        <View style={[styles.shareRow, { marginTop: SPACING.md }]}>
          <View>
            <Text style={styles.shareTitle}>试用密钥</Text>
            <Text style={styles.shareDesc}>分享试用密钥给好友体验</Text>
          </View>
          <ActionButton
            title="分享密钥"
            onPress={() => {
              if (keys.length === 0) {
                Alert.alert('提示', '请先生成密钥');
                return;
              }
              setShowShareModal(true);
            }}
            variant="secondary"
          />
        </View>
      </View>

      {/* Settings */}
      <Text style={styles.sectionTitle}>⚙️ 设置</Text>
      <View style={styles.settingsCard}>
        <View style={styles.settingRow}>
          <Text style={styles.settingLabel}>暗黑模式</Text>
          <Switch
            value={darkMode}
            onValueChange={async (val) => {
              setDarkMode(val);
              await Storage.setTheme(val ? 'dark' : 'light');
            }}
            trackColor={{ true: COLORS.primary, false: COLORS.border }}
          />
        </View>
        <View style={styles.divider} />
        <View style={styles.settingRow}>
          <Text style={styles.settingLabel}>版本</Text>
          <Text style={styles.settingValue}>v1.0.0</Text>
        </View>
      </View>

      {/* About */}
      <View style={styles.aboutSection}>
        <Text style={styles.aboutText}>ModelMux Agent Mobile</Text>
        <Text style={styles.aboutSubText}>统一接入 · 多模型路由 · 共享网络</Text>
      </View>

      {/* Generate Key Modal */}
      <Modal visible={showKeyModal} animationType="slide" transparent>
        <View style={styles.modalOverlay}>
          <View style={styles.modalContent}>
            <Text style={styles.modalTitle}>生成新密钥</Text>
            <Text style={styles.label}>密钥名称</Text>
            <TextInput
              style={styles.input}
              placeholder="例如：我的密钥"
              placeholderTextColor={COLORS.textSecondary}
              value={keyName}
              onChangeText={setKeyName}
            />
            <View style={styles.modalActions}>
              <ActionButton
                title="取消"
                onPress={() => {
                  setShowKeyModal(false);
                  setKeyName('');
                }}
                variant="secondary"
              />
              <ActionButton title="生成" onPress={handleGenerateKey} />
            </View>
          </View>
        </View>
      </Modal>

      {/* Share Modal */}
      <Modal visible={showShareModal} animationType="slide" transparent>
        <View style={styles.modalOverlay}>
          <View style={styles.modalContent}>
            <Text style={styles.modalTitle}>分享试用密钥</Text>
            <Text style={styles.shareInfo}>选择一个密钥分享给好友：</Text>
            {keys.map((key) => (
              <TouchableOpacity
                key={key.id}
                style={styles.shareKeyItem}
                onPress={() => {
                  Alert.alert('分享', `密钥 ${key.name} 的分享链接已生成`);
                  setShowShareModal(false);
                }}
              >
                <Text style={styles.shareKeyName}>{key.name}</Text>
                <Text style={styles.shareKeyMasked}>
                  {key.key.slice(0, 6)}••••{key.key.slice(-4)}
                </Text>
              </TouchableOpacity>
            ))}
            <ActionButton
              title="关闭"
              onPress={() => setShowShareModal(false)}
              variant="secondary"
            />
          </View>
        </View>
      </Modal>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: COLORS.background },
  content: { padding: SPACING.md, paddingBottom: SPACING.xl },
  pageTitle: { fontSize: FONT_SIZE.xxl, fontWeight: '700', color: COLORS.text, marginBottom: SPACING.lg },

  profileCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.lg,
    flexDirection: 'row',
    alignItems: 'center',
    gap: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
    marginBottom: SPACING.lg,
  },
  avatar: {
    width: 56,
    height: 56,
    borderRadius: 28,
    backgroundColor: COLORS.primary,
    alignItems: 'center',
    justifyContent: 'center',
  },
  avatarText: { color: '#FFF', fontSize: FONT_SIZE.xxl, fontWeight: '700' },
  profileInfo: { flex: 1 },
  profileName: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text },
  profileUrl: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, marginTop: 2 },

  sectionTitle: { fontSize: FONT_SIZE.lg, fontWeight: '600', color: COLORS.text, marginTop: SPACING.lg, marginBottom: SPACING.md },

  keyList: { marginBottom: SPACING.md },
  emptyKeys: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.md,
    padding: SPACING.lg,
    alignItems: 'center',
  },
  emptyKeysText: { color: COLORS.textSecondary },
  keyItem: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.md,
    padding: SPACING.md,
    marginBottom: SPACING.sm,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  keyInfo: { flex: 1 },
  keyName: { fontSize: FONT_SIZE.md, fontWeight: '600', color: COLORS.text },
  keyMasked: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, fontFamily: 'monospace', marginTop: 2 },
  keyDate: { fontSize: FONT_SIZE.xs, color: COLORS.textSecondary, marginTop: 2 },
  keyActions: { flexDirection: 'row', gap: SPACING.sm },
  keyBtn: { padding: SPACING.sm },
  keyBtnText: { fontSize: 18 },
  addKeyBtn: {
    backgroundColor: COLORS.surface,
    padding: SPACING.md,
    borderRadius: BORDER_RADIUS.md,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: COLORS.primary,
    borderStyle: 'dashed',
  },
  addKeyBtnText: { color: COLORS.primary, fontWeight: '600', fontSize: FONT_SIZE.md },

  shareCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  shareRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center' },
  shareTitle: { fontSize: FONT_SIZE.md, fontWeight: '600', color: COLORS.text },
  shareDesc: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, marginTop: 2 },

  settingsCard: {
    backgroundColor: COLORS.surface,
    borderRadius: BORDER_RADIUS.lg,
    padding: SPACING.md,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 3,
  },
  settingRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', paddingVertical: SPACING.md },
  settingLabel: { fontSize: FONT_SIZE.md, color: COLORS.text },
  settingValue: { fontSize: FONT_SIZE.md, color: COLORS.textSecondary },
  divider: { height: 1, backgroundColor: COLORS.border },

  aboutSection: { alignItems: 'center', marginTop: SPACING.xl, paddingVertical: SPACING.lg },
  aboutText: { fontSize: FONT_SIZE.md, fontWeight: '600', color: COLORS.text },
  aboutSubText: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, marginTop: 4 },

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
  label: { fontSize: FONT_SIZE.sm, fontWeight: '500', color: COLORS.text, marginBottom: SPACING.xs },
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

  shareInfo: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, marginBottom: SPACING.md },
  shareKeyItem: {
    backgroundColor: COLORS.background,
    padding: SPACING.md,
    borderRadius: BORDER_RADIUS.sm,
    marginBottom: SPACING.sm,
  },
  shareKeyName: { fontSize: FONT_SIZE.md, fontWeight: '500', color: COLORS.text },
  shareKeyMasked: { fontSize: FONT_SIZE.sm, color: COLORS.textSecondary, fontFamily: 'monospace' },
});
