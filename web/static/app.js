// API 基础请求封装
const API = {
    async request(url, options = {}) {
        const token = localStorage.getItem('token') || getCookie('token');
        const headers = {
            'Content-Type': 'application/json',
            ...(token && { 'Authorization': `Bearer ${token}` }),
            ...options.headers
        };
        const response = await fetch(url, { ...options, headers });
        if (response.status === 401) {
            window.location.href = '/login';
            return;
        }
        if (!response.ok) {
            const error = await response.text();
            throw new Error(error || '请求失败');
        }
        return response.json();
    },
    get(url) { return this.request(url, { method: 'GET' }); },
    post(url, data) { return this.request(url, { method: 'POST', body: JSON.stringify(data) }); },
    put(url, data) { return this.request(url, { method: 'PUT', body: JSON.stringify(data) }); },
    delete(url) { return this.request(url, { method: 'DELETE' }); }
};

function getCookie(name) {
    const value = `; ${document.cookie}`;
    const parts = value.split(`; ${name}=`);
    if (parts.length === 2) return parts.pop().split(';').shift();
}

// 通用消息提示
function showMessage(message, type = 'info') {
    alert(message); // 简化，可替换为 toast
}

// 登出
document.addEventListener('DOMContentLoaded', () => {
    const logoutBtn = document.getElementById('logoutBtn');
    if (logoutBtn) {
        logoutBtn.addEventListener('click', (e) => {
            e.preventDefault();
            document.cookie = 'token=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;';
            localStorage.removeItem('token');
            window.location.href = '/login';
        });
    }
});
