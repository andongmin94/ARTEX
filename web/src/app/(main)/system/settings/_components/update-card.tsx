import { DownloadIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function UpdateCard() {
  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          데스크톱 업데이트
        </CardTitle>
        <CardDescription>앱 업데이트는 Electron에서 관리합니다.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <Badge variant="outline">자동 업데이트 미구성</Badge>
        <p className="text-muted-foreground">
          현재 개발 패키지에는 자동 업데이트와 설치 프로그램이 포함되어 있지 않습니다. 서명된 배포 패키지와 업데이트
          검증은 아직 완료하지 않았습니다.
        </p>
      </CardContent>
    </Card>
  );
}
