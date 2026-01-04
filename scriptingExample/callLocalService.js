/**
 * 调用本地 HTTP 服务处理参数内容
 * @author ClipSave
 * @param {string} content - 剪贴板内容，作为 HTTP 请求的 content 参数
 * @returns {string|object} - 服务返回的结果或错误信息
 */

// 检查剪贴板内容是否为文本类型
if (item.ContentType !== "Text") {
    return {
      error: "只支持文本类型的剪贴板内容",
    };
  }
  
  const content = item.Content || "";
  
  if (!content) {
    return {
      error: "剪贴板内容为空",
    };
  }
  
  // ===== 配置区域 =====
  // 本地 HTTP 服务地址和端点（可根据需要修改）
  const serviceHost = "192.168.26.11:6527/clip-save";
  const serviceEndpoint = "your-endpoint"; // 修改为实际的服务端点
  
  // ===== 调用服务 =====
  try {
    // 构建请求 URL，将 content 作为查询参数
    const url = `http://${serviceHost}/${serviceEndpoint}?content=${encodeURIComponent(content)}`;
    
    // 发送 GET 请求
    const response = await fetch(url, {
      method: "GET",
      headers: {
        "Content-Type": "application/json",
      },
    });
  
    if (!response.ok) {
      return {
        error: `请求失败: ${response.status} ${response.statusText}`,
      };
    }
  
    // 解析响应（假设返回 JSON）
    const result = await response.json();
    
    // 检查是否有错误
    if (result.error) {
      return {
        error: result.error,
      };
    }
  
    // 返回 returnValue 或直接返回结果
    if (result.returnValue !== undefined) {
      // 如果 returnValue 是字符串，直接返回；否则转换为字符串
      return typeof result.returnValue === "string" 
        ? result.returnValue 
        : JSON.stringify(result.returnValue);
    }
  
    // 如果没有 returnValue，返回整个结果对象（转换为字符串）
    return JSON.stringify(result);
  } catch (error) {
    return {
      error: `调用本地服务失败: ${error.message || String(error)}`,
    };
  }
  