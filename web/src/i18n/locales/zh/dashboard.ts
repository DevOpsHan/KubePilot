export const zhDashboard = {
  page: {
    title: '运维总览',
    subtitle: '集群健康、待办事项与同步状态一览',
    refresh: '刷新',
    refreshing: '刷新中…',
  },
  statistics: {
    connectedClusters: '接入集群',
    healthyClusters: '健康集群',
    nodes: '节点',
    pods: 'Pod',
    pendingApprovals: '待审批',
    activeAlerts: '活跃告警',
  },
  health: {
    title: '集群健康',
    viewAll: '查看全部',
    clusterCount_one: '{{count}} 个集群',
    clusterCount_other: '{{count}} 个集群',
    healthy: '健康',
    problem: '异常',
    unknown: '未知',
    empty: '暂无集群',
  },
  audit: {
    title: '最近操作',
    openCenter: '审计中心',
    empty: '暂无操作记录',
  },
  sync: {
    title: '同步状态',
    running: '同步进行中',
    idle: '空闲',
    lastFinished: '上次完成',
    nextScheduled: '下次计划',
  },
  quickActions: {
    title: '快捷操作',
  },
  security: {
    title: '安全状态',
    configured: '已配置',
    notConfigured: '未配置',
  },
  model: {
    title: 'AI 模型',
    manage: '管理',
    configured: '已配置',
    notConfigured: '未配置模型',
    unavailable: '模型状态不可用',
  },
  inspection: {
    title: '巡检摘要',
    reports: '近期报告',
    anomalies: '近期异常',
    empty: '暂无巡检记录',
  },
} as const
