import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = { title: "衣橱 · YSM 模型站", description: "上传你的 YSM 模型，在服务器里遇见新的自己。" };
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="zh-CN"><body>{children}</body></html>;
}
