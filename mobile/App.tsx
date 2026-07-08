import React from 'react';
import { NavigationContainer } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { StatusBar, Text } from 'react-native';
import HomeScreen from './screens/HomeScreen';
import ConfigScreen from './screens/ConfigScreen';
import StatsScreen from './screens/StatsScreen';
import NetworkScreen from './screens/NetworkScreen';
import ProfileScreen from './screens/ProfileScreen';
import { COLORS, FONT_SIZE } from './utils/theme';

const Tab = createBottomTabNavigator();

// Simple emoji-based tab icons (no vector-icons dependency needed for MVP)
function TabIcon({ emoji, focused }: { emoji: string; focused: boolean }) {
  return (
    <Text style={{ fontSize: 22, opacity: focused ? 1 : 0.5 }}>{emoji}</Text>
  );
}

export default function App() {
  return (
    <SafeAreaProvider>
      <StatusBar barStyle="dark-content" backgroundColor={COLORS.background} />
      <NavigationContainer>
        <Tab.Navigator
          screenOptions={{
            tabBarActiveTintColor: COLORS.primary,
            tabBarInactiveTintColor: COLORS.textSecondary,
            tabBarStyle: {
              backgroundColor: COLORS.surface,
              borderTopColor: COLORS.border,
              borderTopWidth: 1,
              paddingBottom: 6,
              paddingTop: 6,
              height: 60,
            },
            tabBarLabelStyle: {
              fontSize: FONT_SIZE.xs,
              fontWeight: '500',
            },
            headerStyle: {
              backgroundColor: COLORS.surface,
              elevation: 0,
              shadowOpacity: 0,
              borderBottomWidth: 1,
              borderBottomColor: COLORS.border,
            },
            headerTitleStyle: {
              fontWeight: '600',
              color: COLORS.text,
            },
          }}
        >
          <Tab.Screen
            name="首页"
            component={HomeScreen}
            options={{
              title: 'ModelMux',
              headerTitle: 'ModelMux Agent',
              tabBarIcon: ({ focused }) => <TabIcon emoji="🏠" focused={focused} />,
            }}
          />
          <Tab.Screen
            name="配置"
            component={ConfigScreen}
            options={{
              tabBarIcon: ({ focused }) => <TabIcon emoji="⚙️" focused={focused} />,
            }}
          />
          <Tab.Screen
            name="统计"
            component={StatsScreen}
            options={{
              tabBarIcon: ({ focused }) => <TabIcon emoji="📊" focused={focused} />,
            }}
          />
          <Tab.Screen
            name="网络"
            component={NetworkScreen}
            options={{
              tabBarIcon: ({ focused }) => <TabIcon emoji="🌐" focused={focused} />,
            }}
          />
          <Tab.Screen
            name="我的"
            component={ProfileScreen}
            options={{
              tabBarIcon: ({ focused }) => <TabIcon emoji="👤" focused={focused} />,
            }}
          />
        </Tab.Navigator>
      </NavigationContainer>
    </SafeAreaProvider>
  );
}
