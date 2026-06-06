import {
  Chrome,
  Compass as Firefox,
  Globe,
  Terminal,
  Code, 
  Database, 
  Mail, 
  Music, 
  Image, 
  Video,
  FileText,
  Settings,
  Shield,
  Wifi,
  Monitor,
  Smartphone,
  Gamepad2 as GameController2,
  Headphones,
  Camera,
  Download,
  Upload,
  Cloud,
  Server,
  Cpu,
  HardDrive,
  Zap,
  Activity,
  Bot,
  Puzzle,
  Wrench,
  Package
} from 'lucide-react'

export interface ProcessIconInfo {
  icon: React.ComponentType<any>
  color: string
  name: string
}

// 进程图标映射表
export const processIconMap: Record<string, ProcessIconInfo> = {
  // 浏览器
  'chrome.exe': { icon: Chrome, color: 'text-blue-500', name: 'Google Chrome' },
  'firefox.exe': { icon: Firefox, color: 'text-orange-500', name: 'Mozilla Firefox' },
  'msedge.exe': { icon: Globe, color: 'text-blue-600', name: 'Microsoft Edge' },
  'brave.exe': { icon: Shield, color: 'text-orange-600', name: 'Brave Browser' },
  'opera.exe': { icon: Globe, color: 'text-red-500', name: 'Opera' },
  'safari.exe': { icon: Globe, color: 'text-blue-400', name: 'Safari' },
  
  // 开发工具
  'code.exe': { icon: Code, color: 'text-blue-600', name: 'VS Code' },
  'devenv.exe': { icon: Code, color: 'text-purple-600', name: 'Visual Studio' },
  'idea.exe': { icon: Code, color: 'text-red-600', name: 'IntelliJ IDEA' },
  'pycharm.exe': { icon: Code, color: 'text-green-600', name: 'PyCharm' },
  'webstorm.exe': { icon: Code, color: 'text-cyan-600', name: 'WebStorm' },
  'atom.exe': { icon: Code, color: 'text-green-500', name: 'Atom' },
  'sublime_text.exe': { icon: Code, color: 'text-orange-500', name: 'Sublime Text' },
  'notepad++.exe': { icon: FileText, color: 'text-green-600', name: 'Notepad++' },
  
  // 终端和命令行
  'cmd.exe': { icon: Terminal, color: 'text-black', name: 'Command Prompt' },
  'powershell.exe': { icon: Terminal, color: 'text-blue-700', name: 'PowerShell' },
  'bash.exe': { icon: Terminal, color: 'text-green-700', name: 'Bash' },
  'wsl.exe': { icon: Terminal, color: 'text-orange-600', name: 'WSL' },
  'git.exe': { icon: Terminal, color: 'text-orange-500', name: 'Git' },
  'node.exe': { icon: Zap, color: 'text-green-500', name: 'Node.js' },
  'python.exe': { icon: Code, color: 'text-blue-500', name: 'Python' },
  'java.exe': { icon: Code, color: 'text-red-600', name: 'Java' },
  
  // API工具
  'postman.exe': { icon: Zap, color: 'text-orange-500', name: 'Postman' },
  'insomnia.exe': { icon: Zap, color: 'text-purple-500', name: 'Insomnia' },
  'curl.exe': { icon: Terminal, color: 'text-gray-600', name: 'cURL' },
  'wget.exe': { icon: Download, color: 'text-blue-500', name: 'Wget' },
  'httpie.exe': { icon: Zap, color: 'text-green-500', name: 'HTTPie' },
  
  // 数据库工具
  'mysql.exe': { icon: Database, color: 'text-blue-600', name: 'MySQL' },
  'postgres.exe': { icon: Database, color: 'text-blue-500', name: 'PostgreSQL' },
  'mongodb.exe': { icon: Database, color: 'text-green-600', name: 'MongoDB' },
  'redis.exe': { icon: Database, color: 'text-red-500', name: 'Redis' },
  'sqlite.exe': { icon: Database, color: 'text-gray-600', name: 'SQLite' },
  
  // 网络工具
  'wireshark.exe': { icon: Activity, color: 'text-blue-600', name: 'Wireshark' },
  'fiddler.exe': { icon: Activity, color: 'text-green-600', name: 'Fiddler' },
  'charles.exe': { icon: Activity, color: 'text-orange-600', name: 'Charles' },
  'tcpdump.exe': { icon: Activity, color: 'text-gray-600', name: 'tcpdump' },
  'netstat.exe': { icon: Wifi, color: 'text-blue-500', name: 'Netstat' },
  'ping.exe': { icon: Wifi, color: 'text-green-500', name: 'Ping' },
  'ssh.exe': { icon: Terminal, color: 'text-gray-700', name: 'SSH' },
  
  // 聊天和通讯
  'discord.exe': { icon: Headphones, color: 'text-indigo-500', name: 'Discord' },
  'slack.exe': { icon: Mail, color: 'text-purple-500', name: 'Slack' },
  'teams.exe': { icon: Mail, color: 'text-blue-600', name: 'Microsoft Teams' },
  'zoom.exe': { icon: Camera, color: 'text-blue-500', name: 'Zoom' },
  'skype.exe': { icon: Camera, color: 'text-blue-600', name: 'Skype' },
  
  // 系统工具
  'explorer.exe': { icon: HardDrive, color: 'text-blue-600', name: 'Windows Explorer' },
  'taskmgr.exe': { icon: Activity, color: 'text-red-500', name: 'Task Manager' },
  'services.exe': { icon: Settings, color: 'text-gray-600', name: 'Services' },
  'svchost.exe': { icon: Server, color: 'text-gray-500', name: 'Service Host' },
  'system.exe': { icon: Cpu, color: 'text-red-600', name: 'System' },
  'csrss.exe': { icon: Monitor, color: 'text-blue-500', name: 'Client Server Runtime' },
  
  // 游戏平台
  'steam.exe': { icon: GameController2, color: 'text-blue-600', name: 'Steam' },
  'epic.exe': { icon: GameController2, color: 'text-gray-700', name: 'Epic Games' },
  'battle.net.exe': { icon: GameController2, color: 'text-blue-500', name: 'Battle.net' },
  
  // 多媒体
  'vlc.exe': { icon: Video, color: 'text-orange-500', name: 'VLC Media Player' },
  'spotify.exe': { icon: Music, color: 'text-green-500', name: 'Spotify' },
  'itunes.exe': { icon: Music, color: 'text-gray-700', name: 'iTunes' },
  'photoshop.exe': { icon: Image, color: 'text-blue-600', name: 'Adobe Photoshop' },
  
  // 云服务
  'dropbox.exe': { icon: Cloud, color: 'text-blue-500', name: 'Dropbox' },
  'onedrive.exe': { icon: Cloud, color: 'text-blue-600', name: 'OneDrive' },
  'googledrive.exe': { icon: Cloud, color: 'text-blue-500', name: 'Google Drive' },
  'icloud.exe': { icon: Cloud, color: 'text-gray-600', name: 'iCloud' },
  
  // 构建工具
  'npm.exe': { icon: Package, color: 'text-red-500', name: 'NPM' },
  'yarn.exe': { icon: Package, color: 'text-blue-500', name: 'Yarn' },
  'docker.exe': { icon: Package, color: 'text-blue-600', name: 'Docker' },
  'kubectl.exe': { icon: Server, color: 'text-blue-500', name: 'Kubernetes' },
  'terraform.exe': { icon: Wrench, color: 'text-purple-500', name: 'Terraform' },
  
  // WebSocket工具
  'wscat.exe': { icon: Zap, color: 'text-green-600', name: 'wscat' },
  'websocket.exe': { icon: Activity, color: 'text-purple-500', name: 'WebSocket Tool' },
  
  // 移动开发
  'adb.exe': { icon: Smartphone, color: 'text-green-600', name: 'Android Debug Bridge' },
  'simulator.exe': { icon: Smartphone, color: 'text-blue-500', name: 'iOS Simulator' },
  
  // 自定义应用
  'myapp.exe': { icon: Puzzle, color: 'text-purple-500', name: 'My Application' },
  'testapp.exe': { icon: Bot, color: 'text-cyan-500', name: 'Test Application' },
}

// 默认图标信息
export const defaultProcessIcon: ProcessIconInfo = {
  icon: Monitor,
  color: 'text-gray-500',
  name: 'Unknown Process'
}

/**
 * 根据进程名称获取图标信息
 * @param processName 进程名称
 * @returns 图标信息
 */
export function getProcessIcon(processName?: string): ProcessIconInfo {
  if (!processName) {
    return defaultProcessIcon
  }
  
  // 转换为小写进行匹配
  const normalizedName = processName.toLowerCase()
  
  // 直接匹配
  if (processIconMap[normalizedName]) {
    return processIconMap[normalizedName]
  }
  
  // 模糊匹配（去除版本号等）
  for (const [key, iconInfo] of Object.entries(processIconMap)) {
    const baseKey = key.replace('.exe', '').toLowerCase()
    const baseName = normalizedName.replace('.exe', '').toLowerCase()
    
    if (baseName.includes(baseKey) || baseKey.includes(baseName)) {
      return iconInfo
    }
  }
  
  // 特殊匹配规则
  if (normalizedName.includes('chrome')) {
    return processIconMap['chrome.exe']
  }
  if (normalizedName.includes('firefox')) {
    return processIconMap['firefox.exe']
  }
  if (normalizedName.includes('edge')) {
    return processIconMap['msedge.exe']
  }
  if (normalizedName.includes('code')) {
    return processIconMap['code.exe']
  }
  if (normalizedName.includes('node')) {
    return processIconMap['node.exe']
  }
  if (normalizedName.includes('python')) {
    return processIconMap['python.exe']
  }
  if (normalizedName.includes('java')) {
    return processIconMap['java.exe']
  }
  
  return defaultProcessIcon
}

/**
 * 获取所有支持的进程类型
 * @returns 支持的进程名称列表
 */
export function getSupportedProcesses(): string[] {
  return Object.keys(processIconMap)
}

/**
 * 按类别获取进程图标
 * @returns 按类别分组的进程图标
 */
export function getProcessIconsByCategory() {
  return {
    browsers: ['chrome.exe', 'firefox.exe', 'msedge.exe', 'brave.exe', 'opera.exe', 'safari.exe'],
    development: ['code.exe', 'devenv.exe', 'idea.exe', 'pycharm.exe', 'webstorm.exe', 'atom.exe'],
    terminal: ['cmd.exe', 'powershell.exe', 'bash.exe', 'wsl.exe', 'git.exe'],
    apiTools: ['postman.exe', 'insomnia.exe', 'curl.exe', 'wget.exe', 'httpie.exe'],
    databases: ['mysql.exe', 'postgres.exe', 'mongodb.exe', 'redis.exe', 'sqlite.exe'],
    networking: ['wireshark.exe', 'fiddler.exe', 'charles.exe', 'tcpdump.exe', 'netstat.exe'],
    system: ['explorer.exe', 'taskmgr.exe', 'services.exe', 'svchost.exe', 'system.exe']
  }
}
