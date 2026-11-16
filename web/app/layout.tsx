import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = { title: "衣橱 · 麦块 YSM 上传管理", description: "适用于 Minekuai 麦块面板服的 YSM 上传管理工具，通过 SFTP 上传模型并调用面板 API 申请重载。" };
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="zh-CN"><body>{children}</body></html>;
}
